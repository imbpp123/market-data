package upstream

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapturePreservesTransportErrorThroughSDKWrapping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), Bybit, recorder)
		ctx := begin(t, c, Bybit, Tickers)
		_, err := Execute(ctx, func(ctx context.Context) error {
			_, err := send(ctx, transport, "/unknown?category=spot")
			require.Error(t, err)
			return errors.New("SDK error without wrapping")
		})
		assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
		assert.Empty(t, recorder.sent())
	})
}

func TestCaptureRejectsSDKDecodeFailuresAndMissingRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), Bybit, recorder)
		ctx := begin(t, c, Bybit, Tickers)
		_, err := Execute(ctx, func(ctx context.Context) error {
			_, err := send(ctx, transport, "/v5/market/tickers?category=spot")
			require.NoError(t, err)
			return errors.New("invalid SDK model")
		})
		assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		assert.Equal(t, 1, c.Attempts(ctx))
		_, err = Execute(ctx, func(context.Context) error { return nil })
		assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
	})
}

func TestCanceledReservationRestoresBudgetAndSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, smallBybitConfig(), Bybit, recorder)
		ctx := begin(t, c, Bybit, Instruments)
		operation, err := c.operation(ctx, Bybit, Instruments)
		require.NoError(t, err)
		reservation, err := c.acquire(ctx, Bybit, cost{operation: Instruments}, operation, c.clock.Now())
		require.NoError(t, err)
		// A canceled reservation has not reached a transport and must be reversible.
		c.rollback(Bybit, operation, reservation)
		assert.Zero(t, c.Attempts(ctx))
		_, err = send(ctx, transport, "/v5/market/instruments-info?category=spot")
		require.NoError(t, err)
		sent := recorder.sent()
		require.Len(t, sent, 1)
		assert.Equal(t, reservation.at, sent[0].at)
		assert.Equal(t, 1, c.Attempts(ctx))
		assert.Zero(t, c.active)
	})
}
