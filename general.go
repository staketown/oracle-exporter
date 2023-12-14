package main

import (
	"context"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	oracletypes "github.com/umee-network/umee/v6/x/oracle/types"
	"google.golang.org/grpc"
)

func GeneralHandler(w http.ResponseWriter, r *http.Request, grpcConn *grpc.ClientConn, blockTime uint64) {
	requestStart := time.Now()

	sublogger := log.With().
		Str("request-id", uuid.New().String()).
		Logger()

	valoper := r.URL.Query().Get("valoper")
	myAddress, err := sdk.ValAddressFromBech32(valoper)

	if err != nil {
		sublogger.Error().
			Str("valoper", valoper).
			Err(err).
			Msg("Could not get validator address")
		return
	}

	generalWindowProgressGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "window_progress",
			Help:        "Current slash window progress, block number in the current window",
			ConstLabels: ConstLabels,
		},
	)

	generalWindowSizeGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "window_size",
			Help:        "Current window size",
			ConstLabels: ConstLabels,
		},
	)

	paramsSlashWindowGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "slash_window",
			Help:        "Number of blocks during which validators can miss votes",
			ConstLabels: ConstLabels,
		},
	)

	paramsMinValidPerWindowGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "min_valid_per_window",
			Help:        "Percentage of misses triggering a slash at the end of the slash window",
			ConstLabels: ConstLabels,
		},
	)

	paramsSlashFractionGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "slash_fraction",
			Help:        "Slash fraction",
			ConstLabels: ConstLabels,
		},
	)

	paramsVotePeriodGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "vote_period",
			Help:        "Number of block to submit the next vote",
			ConstLabels: ConstLabels,
		},
	)

	paramsSymbolsCountGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "symbols_count",
			Help:        "Number of symbols the feeder is supposed to broadcast",
			ConstLabels: ConstLabels,
		},
	)

	validatorMissCounterGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "miss_counter",
			Help:        "Current miss counter for a given validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	validatorAggregateVoteGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "aggregated_votes",
			Help:        "Current aggregate vote for a given validator",
			ConstLabels: ConstLabels,
		},
		[]string{"asset"},
	)

	validatorFeederAccountGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "feeder_account",
			Help:        "Account delegated account for a given validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper", "feeder"},
	)

	validatorMissRateGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "miss_rate",
			Help:        "Current miss rate for given validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	validatorNextWindowStartGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "next_window_start",
			Help:        "Timestamp of the next estimated windows start in UTC",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)
	validatorLastBlockVoteGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "last_block_vote",
			Help:        "Last block validator voted",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(generalWindowProgressGauge)
	registry.MustRegister(generalWindowSizeGauge)
	registry.MustRegister(paramsSlashWindowGauge)
	registry.MustRegister(paramsMinValidPerWindowGauge)
	registry.MustRegister(paramsSlashFractionGauge)
	registry.MustRegister(paramsVotePeriodGauge)
	registry.MustRegister(paramsSymbolsCountGauge)
	registry.MustRegister(validatorMissCounterGauge)
	registry.MustRegister(validatorFeederAccountGauge)
	registry.MustRegister(validatorMissRateGauge)
	registry.MustRegister(validatorNextWindowStartGauge)
	registry.MustRegister(validatorLastBlockVoteGauge)

	registry.MustRegister(validatorAggregateVoteGauge)

	// doing this not in goroutine as we'll need slash window value later
	sublogger.Debug().Msg("Started querying current slash window progress")
	slashWindowQueryStart := time.Now()

	oracleClient := oracletypes.NewQueryClient(grpcConn)
	slashWindowResponse, err := oracleClient.SlashWindow(
		context.Background(),
		&oracletypes.QuerySlashWindow{},
	)
	if err != nil {
		sublogger.Error().Err(err).Msg("Could not get current slash window progress")
		return
	}

	sublogger.Debug().
		Float64("request-time", time.Since(slashWindowQueryStart).Seconds()).
		Msg("Finished querying current slash window progress")

	generalWindowProgressGauge.Set(float64(slashWindowResponse.WindowProgress))

	// doing this not in goroutine as we'll need params from oracle params response for calculation
	sublogger.Debug().Msg("Started querying oracle params")
	queryStart := time.Now()

	oracleParamsResponse, err := oracleClient.Params(
		context.Background(),
		&oracletypes.QueryParams{},
	)
	if err != nil {
		sublogger.Error().
			Err(err).
			Msg("Could not get oracle params")
		return
	}

	sublogger.Debug().
		Float64("request-time", time.Since(queryStart).Seconds()).
		Msg("Finished querying oracle params")

	windowSize := oracleParamsResponse.Params.SlashWindow / oracleParamsResponse.Params.VotePeriod
	generalWindowSizeGauge.Set(float64(windowSize))
	paramsSlashWindowGauge.Set(float64(oracleParamsResponse.Params.SlashWindow))
	paramsMinValidPerWindowGauge.Set(oracleParamsResponse.Params.MinValidPerWindow.MustFloat64())
	paramsSlashFractionGauge.Set(oracleParamsResponse.Params.SlashFraction.MustFloat64())
	paramsVotePeriodGauge.Set(float64(oracleParamsResponse.Params.VotePeriod))
	paramsSymbolsCountGauge.Set(float64(len(oracleParamsResponse.Params.AcceptList)))

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started querying validator current miss counter")
		queryStart := time.Now()

		oracleClient := oracletypes.NewQueryClient(grpcConn)
		missCounterResponse, err := oracleClient.MissCounter(
			context.Background(),
			&oracletypes.QueryMissCounter{ValidatorAddr: myAddress.String()},
		)
		if err != nil {
			sublogger.Error().
				Str("valoper", valoper).
				Err(err).
				Msg("Could not get validator current miss counter")
			return
		}

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying validator current miss counter")

		validatorMissCounterGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(float64(missCounterResponse.MissCounter))

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started calculate the miss rate")
		missRateStart := time.Now()

		missRate := float64(missCounterResponse.MissCounter) / float64(slashWindowResponse.WindowProgress)

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(missRateStart).Seconds()).
			Msg("Finished calculate the miss rate")

		validatorMissRateGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(missRate)

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started calculate calculate the estimated windows start")
		windowStart := time.Now()

		oracleParams := oracleParamsResponse.Params
		seconds := (windowSize - slashWindowResponse.WindowProgress + 1) * blockTime * oracleParams.VotePeriod

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(windowStart).Seconds()).
			Msg("Finished calculate the estimated windows start")

		validatorNextWindowStartGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(float64(time.Now().Add(time.Duration(seconds) * time.Second).UTC().UnixMilli()))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started querying feeder account associated with the validator")
		queryStart := time.Now()

		oracleClient := oracletypes.NewQueryClient(grpcConn)
		response, err := oracleClient.FeederDelegation(
			context.Background(),
			&oracletypes.QueryFeederDelegation{ValidatorAddr: myAddress.String()},
		)
		if err != nil {
			sublogger.Error().
				Str("valoper", valoper).
				Err(err).
				Msg("Could not get feeder account associated with the validator")
			return
		}

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying feeder account associated with the validator")

		validatorFeederAccountGauge.With(prometheus.Labels{
			"valoper": valoper,
			"feeder":  response.FeederAddr,
		}).Set(1)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started querying validator prevote aggregate")
		queryStart := time.Now()

		oracleClient := oracletypes.NewQueryClient(grpcConn)
		response, err := oracleClient.AggregatePrevote(
			context.Background(),
			&oracletypes.QueryAggregatePrevote{ValidatorAddr: myAddress.String()},
		)
		if err != nil {
			sublogger.Warn().
				Str("valoper", valoper).
				Err(err).
				Msg("Could not get validator prevote aggregate")
			return
		}

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying validator prevote aggregate")

		validatorLastBlockVoteGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(float64(response.AggregatePrevote.SubmitBlock))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started querying validator aggregate vote")
		queryStart := time.Now()

		oracleClient := oracletypes.NewQueryClient(grpcConn)
		response, err := oracleClient.AggregateVote(
			context.Background(),
			&oracletypes.QueryAggregateVote{ValidatorAddr: myAddress.String()},
		)
		if err != nil {
			sublogger.Warn().
				Str("valoper", valoper).
				Err(err).
				Msg("Could not get validator aggregate vote")
			return
		}

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying validator aggregate vote")

		for _, asset := range oracleParamsResponse.Params.AcceptList {
			var isContains float64 = 1 // expected that is missed by default

			for _, exchangeTuple := range response.AggregateVote.ExchangeRateTuples {
				if strings.EqualFold(asset.SymbolDenom, exchangeTuple.Denom) {
					isContains = 0 // no misses
					break
				}
			}

			validatorAggregateVoteGauge.With(prometheus.Labels{
				"asset": asset.SymbolDenom,
			}).Set(isContains)
		}
	}()

	wg.Wait()

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	sublogger.Info().
		Str("method", "GET").
		Str("endpoint", "/metrics/general?valoper="+valoper).
		Float64("request-time", time.Since(requestStart).Seconds()).
		Msg("Request processed")
}
