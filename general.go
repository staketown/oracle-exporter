package main

import (
	"context"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	oracletypes "github.com/umee-network/umee/v6/x/oracle/types"
	"google.golang.org/grpc"
)

func GeneralHandler(w http.ResponseWriter, r *http.Request, grpcConn *grpc.ClientConn) {
	requestStart := time.Now()

	sublogger := log.With().
		Str("request-id", uuid.New().String()).
		Logger()

	valoper := r.URL.Query().Get("valoper")
	myAddress, err := sdk.ValAddressFromBech32(valoper)

	generalWindowProgressGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "window_progress",
			Help:        "current slash window progress, block number in the current window",
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
			Name:        "minvalidperwindow",
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

	if err != nil {
		sublogger.Error().
			Str("valoper", valoper).
			Err(err).
			Msg("Could not get validator address")
		return
	}

	validatorMissCounterGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "miss_counter",
			Help:        "Delegations of the Cosmos-based blockchain validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	validatorFeederAccountGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "feeder_account",
			Help:        "Tokens of the Cosmos-based blockchain validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper", "feeder"},
	)

	validatorMissRateGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "miss_rate",
			Help:        "Delegators shares of the Cosmos-based blockchain validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	validatorNextWindowStartGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "next_window_start",
			Help:        "Commission rate of the Cosmos-based blockchain validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)
	validatorLastBlockVoteGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "last_block_vote",
			Help:        "Commission of the Cosmos-based blockchain validator",
			ConstLabels: ConstLabels,
		},
		[]string{"valoper"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(generalWindowProgressGauge)
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

		missRate := missCounterResponse.MissCounter /
			(slashWindowResponse.WindowProgress * uint64(len(oracleParamsResponse.Params.AcceptList)))

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(missRateStart).Seconds()).
			Msg("Finished calculate the miss rate")

		validatorMissRateGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(float64(missRate))

		sublogger.Debug().
			Str("valoper", valoper).
			Msg("Started calculate calculate the estimated windows start")
		windowStart := time.Now()

		oracleParams := oracleParamsResponse.Params
		var blockTime uint64 = 6
		seconds := ((oracleParams.SlashWindow / oracleParams.VotePeriod) - slashWindowResponse.WindowProgress + 1) * blockTime * oracleParams.VotePeriod

		sublogger.Debug().
			Str("valoper", valoper).
			Float64("request-time", time.Since(windowStart).Seconds()).
			Msg("Finished calculate the estimated windows start")

		validatorNextWindowStartGauge.With(prometheus.Labels{
			"valoper": valoper,
		}).Set(float64(time.Now().Add(time.Duration(seconds) * time.Second).UTC().Unix()))
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

	wg.Wait()

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	sublogger.Info().
		Str("method", "GET").
		Str("endpoint", "/metrics/general?valoper="+valoper).
		Float64("request-time", time.Since(requestStart).Seconds()).
		Msg("Request processed")
}
