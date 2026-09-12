package kline_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/observability"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type attemptKey struct{}

type testOperations struct{ maximum int }

func (o testOperations) Run(ctx context.Context, _ application.Scope, observe func(), run func(context.Context) error) error {
	attempts := 0
	attempt := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempts == o.maximum {
			return application.ErrUpstreamAttemptLimit
		}
		attempts++
		observe()
		return nil
	}
	return run(context.WithValue(ctx, attemptKey{}, attempt))
}

type testProvider struct {
	exchange domain.Exchange
	mu       sync.Mutex
	requests []kline.Request
	fetch    func(context.Context, kline.Request) ([]kline.Stored, error)
}

func (p *testProvider) Exchange() domain.Exchange { return p.exchange }

func (*testProvider) SupportedTimeframes(domain.Market) ([]domain.Timeframe, error) {
	return []domain.Timeframe{domain.Timeframe1m, domain.Timeframe1M}, nil
}

func (p *testProvider) GetKlines(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
	if err := ctx.Value(attemptKey{}).(func() error)(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.fetch != nil {
		return p.fetch(ctx, request)
	}
	return pageRows(request, time.Now()), nil
}

func (p *testProvider) calls() []kline.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]kline.Request(nil), p.requests...)
}

func pageRows(request kline.Request, started time.Time) []kline.Stored {
	calendar, _ := domain.NewCalendar(request.Exchange, request.Market, request.Interval)
	var rows []kline.Stored
	for open := request.From; open.Before(request.To); {
		next, _ := calendar.Next(open)
		rows = append(rows, kline.Stored{Candle: domain.Kline{
			Exchange: request.Exchange, Market: request.Market, Symbol: request.Symbol, Interval: request.Interval,
			OpenTime: open, CloseTime: next, FetchedAt: time.Now(),
			Open: decimal.NewFromInt(1), High: decimal.NewFromInt(2), Low: decimal.NewFromInt(1), Close: decimal.NewFromInt(2),
		}, RequestStartedAt: started})
		open = next
	}
	return rows
}

type harness struct {
	service    *kline.Service
	provider   *testProvider
	repository kline.Repository
	catalog    instrument.Repository
	metrics    *observability.Klines
	query      kline.Query
	cancel     context.CancelFunc
}

func serviceSettings() kline.Settings {
	return kline.Settings{HistoryCandles: 1000, MaxCallers: 64, MaxActiveFills: 12, MaxActiveFillsPerExchange: 6, MaxAttempts: 12, FillTimeout: 30 * time.Second}
}

func newHarness(t *testing.T, settings kline.Settings, pageLimit int) *harness {
	t.Helper()
	root, cancel := context.WithCancel(t.Context())
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	provider := &testProvider{exchange: scope.Exchange}
	repository, err := memory.NewKlineRepository(settings.HistoryCandles, time.Now)
	require.NoError(t, err)
	catalog := memory.NewInstrumentRepository()
	require.NoError(t, catalog.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{
		{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"},
		{Exchange: scope.Exchange, Market: scope.Market, Symbol: "ETHUSDT"},
	}))
	metrics := observability.NewKlines()
	service, err := kline.NewService(root, repository, catalog, testOperations{settings.MaxAttempts}, []kline.ScopeSettings{{Scope: scope, Provider: provider, PageLimit: pageLimit}}, settings, time.Now, metrics.Observe)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		service.Wait()
	})
	end := time.Now().UTC().Truncate(time.Minute)
	return &harness{service: service, provider: provider, repository: repository, catalog: catalog, metrics: metrics, cancel: cancel,
		query: kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: end.Add(-4 * time.Minute), To: end}}
}

type outcome struct {
	rows []domain.Kline
	err  error
}

func startGet(ctx context.Context, service *kline.Service, query kline.Query) <-chan outcome {
	result := make(chan outcome, 1)
	go func() {
		rows, err := service.Get(ctx, query)
		result <- outcome{rows: rows, err: err}
	}()
	return result
}

func TestServiceColdWarmAndPartialCache(t *testing.T) {
	cases := []struct {
		name  string
		seed  int
		calls int
	}{
		{"cold", 0, 2}, {"partial", 2, 1}, {"warm", 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 2)
				seed := h.query
				seed.To = seed.From.Add(time.Duration(tc.seed) * time.Minute)
				require.NoError(t, h.repository.UpsertMany(t.Context(), pageRows(kline.Request{Query: seed}, time.Now())))

				rows, err := h.service.Get(t.Context(), h.query)

				require.NoError(t, err)
				require.Len(t, rows, 4)
				assert.Equal(t, h.query.From, rows[0].OpenTime)
				assert.Equal(t, h.query.To, rows[3].CloseTime)
				assert.Len(t, h.provider.calls(), tc.calls)
				again, err := h.service.Get(t.Context(), h.query)
				require.NoError(t, err)
				assert.Equal(t, rows, again)
				assert.Len(t, h.provider.calls(), tc.calls)
				stats := h.metrics.Snapshot()[h.query.Scope]
				assert.Equal(t, uint64(tc.calls), stats.Attempts)
				assert.Equal(t, uint64(4-tc.seed), stats.Downloaded)
			})
		})
	}
}

func TestServiceFiftyIdenticalMissesShareOnePagedFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 2)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		results := make([]<-chan outcome, 50)
		for i := range results {
			results[i] = startGet(t.Context(), h.service, h.query)
		}
		synctest.Wait()
		assert.Equal(t, uint64(49), h.metrics.Snapshot()[h.query.Scope].SharedWaits)

		close(release)

		for _, result := range results {
			got := <-result
			require.NoError(t, got.err)
			require.Len(t, got.rows, 4)
			assert.Equal(t, h.query.From, got.rows[0].OpenTime)
			assert.Equal(t, h.query.To, got.rows[3].CloseTime)
		}
		assert.Len(t, h.provider.calls(), 2)
		assert.Equal(t, uint64(1), h.metrics.Snapshot()[h.query.Scope].Fills)
	})
}

func TestServiceOverlapReusesSavedRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 10)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		first := h.query
		first.To = first.To.Add(-2 * time.Minute)
		second := h.query
		second.From = second.From.Add(time.Minute)
		a := startGet(t.Context(), h.service, first)
		synctest.Wait()
		b := startGet(t.Context(), h.service, second)
		synctest.Wait()

		close(release)

		ra, rb := <-a, <-b
		require.NoError(t, ra.err)
		require.NoError(t, rb.err)
		require.Len(t, ra.rows, 2)
		require.Len(t, rb.rows, 3)
		assert.Equal(t, second.From, rb.rows[0].OpenTime)
		calls := h.provider.calls()
		require.Len(t, calls, 2)
		assert.Equal(t, first, calls[0].Query)
		assert.Equal(t, first.To, calls[1].From)
		assert.Equal(t, second.To, calls[1].To)
	})
}

func TestServiceFinalHitAndOtherSeriesDoNotWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 10)
		warm := h.query
		warm.To = warm.From.Add(time.Minute)
		require.NoError(t, h.repository.UpsertMany(t.Context(), pageRows(kline.Request{Query: warm}, time.Now())))
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			if request.Symbol == "BTCUSDT" {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return pageRows(request, time.Now()), nil
		}
		blocked := startGet(t.Context(), h.service, h.query)
		synctest.Wait()

		rows, err := h.service.Get(t.Context(), warm)

		require.NoError(t, err)
		assert.Len(t, rows, 1)
		other := h.query
		other.Symbol = "ETHUSDT"
		rows, err = h.service.Get(t.Context(), other)
		require.NoError(t, err)
		assert.Len(t, rows, 4)
		close(release)
		require.NoError(t, (<-blocked).err)
	})
}

func TestServiceCallerCancellationDoesNotCancelFill(t *testing.T) {
	cases := []struct {
		name                       string
		cancelLeader, cancelWaiter bool
	}{
		{"leader", true, false}, {"waiter", false, true}, {"all callers", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				release := make(chan struct{})
				h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
					select {
					case <-release:
						return pageRows(request, time.Now()), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				leader, cancelLeader := context.WithCancel(t.Context())
				defer cancelLeader()
				waiter, cancelWaiter := context.WithCancel(t.Context())
				defer cancelWaiter()
				a := startGet(leader, h.service, h.query)
				synctest.Wait()
				b := startGet(waiter, h.service, h.query)
				synctest.Wait()

				if tc.cancelLeader {
					cancelLeader()
				}
				if tc.cancelWaiter {
					cancelWaiter()
				}
				synctest.Wait()
				close(release)

				for _, entry := range []struct {
					result   <-chan outcome
					canceled bool
				}{{a, tc.cancelLeader}, {b, tc.cancelWaiter}} {
					got := <-entry.result
					if entry.canceled {
						assert.ErrorIs(t, got.err, context.Canceled)
					} else {
						require.NoError(t, got.err)
						assert.Len(t, got.rows, 4)
					}
				}
				synctest.Wait()
				rows, err := h.service.Get(t.Context(), h.query)
				require.NoError(t, err)
				assert.Len(t, rows, 4)
				assert.Len(t, h.provider.calls(), 1)
			})
		})
	}
}

func TestServiceFillFailureHasNoRetryBurstAndPreservesPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 2)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if request.From.After(h.query.From) {
				return nil, application.ErrUpstream
			}
			return pageRows(request, time.Now()), nil
		}
		leader := startGet(t.Context(), h.service, h.query)
		synctest.Wait()
		results := make([]<-chan outcome, 49)
		for i := range results {
			results[i] = startGet(t.Context(), h.service, h.query)
		}
		small := h.query
		small.To = small.From.Add(2 * time.Minute)
		complete := startGet(t.Context(), h.service, small)
		synctest.Wait()

		close(release)

		results = append(results, leader)
		for _, result := range results {
			got := <-result
			assert.ErrorIs(t, got.err, application.ErrUpstream)
			assert.Nil(t, got.rows)
		}
		got := <-complete
		require.NoError(t, got.err)
		assert.Len(t, got.rows, 2)
		stored, err := h.repository.GetRange(t.Context(), h.query)
		require.NoError(t, err)
		assert.Len(t, stored, 2)
		assert.Len(t, h.provider.calls(), 2)
	})
}

func TestServiceEmptyAndShortResponsesEndWithoutInventingRows(t *testing.T) {
	cases := []struct {
		name         string
		firstPage    bool
		calls, saved int
	}{
		{"empty", false, 1, 0}, {"short then no progress", true, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				h.provider.fetch = func(_ context.Context, request kline.Request) ([]kline.Stored, error) {
					if tc.firstPage && request.From.Equal(h.query.From) {
						return pageRows(request, time.Now())[:1], nil
					}
					return nil, nil
				}

				rows, err := h.service.Get(t.Context(), h.query)

				assert.ErrorIs(t, err, application.ErrIncompleteData)
				assert.Nil(t, rows)
				stored, err := h.repository.GetRange(t.Context(), h.query)
				require.NoError(t, err)
				assert.Len(t, stored, tc.saved)
				assert.Len(t, h.provider.calls(), tc.calls)
			})
		})
	}
}

func TestServiceOpenCandleSharedOnceAndNextCallerRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 10)
		h.query.From, h.query.To = h.query.To, h.query.To.Add(time.Minute)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		a := startGet(t.Context(), h.service, h.query)
		synctest.Wait()
		b := startGet(t.Context(), h.service, h.query)
		synctest.Wait()
		close(release)
		for _, result := range []<-chan outcome{a, b} {
			got := <-result
			require.NoError(t, got.err)
			assert.Len(t, got.rows, 1)
		}
		assert.Len(t, h.provider.calls(), 1)

		rows, err := h.service.Get(t.Context(), h.query)

		require.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.Len(t, h.provider.calls(), 2)
	})
}

func TestServiceCrossingCloseRequiresNewAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 10)
		h.query.From, h.query.To = h.query.To, h.query.To.Add(time.Minute)
		time.Sleep(59 * time.Second)
		h.provider.fetch = func(_ context.Context, request kline.Request) ([]kline.Stored, error) {
			started := time.Now()
			if started.Before(request.To) {
				time.Sleep(2 * time.Second)
			}
			return pageRows(request, started), nil
		}

		rows, err := h.service.Get(t.Context(), h.query)

		require.NoError(t, err)
		assert.Len(t, rows, 1)
		assert.Len(t, h.provider.calls(), 2)
		stored, err := h.repository.GetRange(t.Context(), h.query)
		require.NoError(t, err)
		assert.False(t, stored[0].RequestStartedAt.Before(h.query.To))
	})
}

func TestServiceDeadlinesAndShutdown(t *testing.T) {
	cases := []struct {
		name           string
		callerDeadline bool
		shutdown       bool
		want           error
	}{
		{"fill deadline", false, false, context.DeadlineExceeded},
		{"caller deadline", true, false, context.DeadlineExceeded},
		{"shutdown", false, true, context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				h.provider.fetch = func(ctx context.Context, _ kline.Request) ([]kline.Stored, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				ctx := t.Context()
				if tc.callerDeadline {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
				}
				result := startGet(ctx, h.service, h.query)
				synctest.Wait()
				if tc.shutdown {
					h.cancel()
				}

				got := <-result

				assert.ErrorIs(t, got.err, tc.want)
				assert.Nil(t, got.rows)
				h.cancel()
				h.service.Wait()
			})
		})
	}
}

func TestServiceRejectsOverloadAndReleasesCapacity(t *testing.T) {
	cases := []struct {
		name                      string
		callers, global, exchange int
	}{
		{"callers", 1, 12, 6}, {"global fills", 64, 1, 6}, {"exchange fills", 64, 12, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				settings := serviceSettings()
				settings.MaxCallers, settings.MaxActiveFills, settings.MaxActiveFillsPerExchange = tc.callers, tc.global, tc.exchange
				h := newHarness(t, settings, 10)
				release := make(chan struct{})
				h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
					select {
					case <-release:
						return pageRows(request, time.Now()), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				first := startGet(t.Context(), h.service, h.query)
				synctest.Wait()
				other := h.query
				other.Symbol = "ETHUSDT"

				rows, err := h.service.Get(t.Context(), other)

				assert.ErrorIs(t, err, application.ErrServiceOverloaded)
				assert.Nil(t, rows)
				close(release)
				require.NoError(t, (<-first).err)
				rows, err = h.service.Get(t.Context(), other)
				require.NoError(t, err)
				assert.Len(t, rows, 4)
			})
		})
	}
}

func TestServiceRejectsImpossiblePlanBeforeFetching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.MaxAttempts = 1
		h := newHarness(t, settings, 2)

		rows, err := h.service.Get(t.Context(), h.query)

		assert.ErrorIs(t, err, application.ErrUpstreamAttemptLimit)
		assert.Nil(t, rows)
		assert.Empty(t, h.provider.calls())
	})
}

func TestServiceCallerAttemptBudgetSurvivesOverlappingFills(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.MaxAttempts = 2
		h := newHarness(t, settings, 1)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		first := h.query
		first.To = first.From.Add(2 * time.Minute)
		leader := startGet(t.Context(), h.service, first)
		synctest.Wait()
		// The follower joins during the first attempt. Both shared pages
		// count, so a new fill cannot reset its exhausted allowance.
		second := h.query
		second.From = first.To
		follower := startGet(t.Context(), h.service, second)
		synctest.Wait()
		close(release)

		require.NoError(t, (<-leader).err)
		got := <-follower

		assert.ErrorIs(t, got.err, application.ErrUpstreamAttemptLimit)
		assert.Nil(t, got.rows)
	})
}

func TestServiceRetentionExpiresWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.HistoryCandles = 4
		h := newHarness(t, settings, 10)
		time.Sleep(59 * time.Second)
		h.provider.fetch = func(_ context.Context, request kline.Request) ([]kline.Stored, error) {
			started := time.Now()
			time.Sleep(2 * time.Second)
			return pageRows(request, started), nil
		}

		rows, err := h.service.Get(t.Context(), h.query)

		assert.ErrorIs(t, err, application.ErrRangeOutOfRetention)
		assert.Nil(t, rows)
		assert.Len(t, h.provider.calls(), 1)
	})
}

func TestServiceValidatesBeforeStorage(t *testing.T) {
	cases := []struct {
		name   string
		change func(*kline.Query)
		want   error
	}{
		{"too many slots", func(q *kline.Query) { q.From = q.To.Add(-1001 * time.Minute) }, application.ErrRequestTooLarge},
		{"old single slot", func(q *kline.Query) {
			q.From = q.To.Add(-1001 * time.Minute)
			q.To = q.From.Add(time.Minute)
		}, application.ErrRangeOutOfRetention},
		{"unsupported", func(q *kline.Query) { q.Interval = domain.Timeframe1s }, application.ErrInvalidInterval},
		{"bad symbol", func(q *kline.Query) { q.Symbol = " BTC" }, application.ErrInvalidFilter},
		{"disabled scope", func(q *kline.Query) { q.Market = domain.MarketLinear }, application.ErrInvalidFilter},
		{"unaligned", func(q *kline.Query) { q.From = q.From.Add(time.Second) }, application.ErrInvalidRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				tc.change(&h.query)
				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				rows, err := h.service.Get(ctx, h.query)

				assert.ErrorIs(t, err, tc.want)
				assert.Nil(t, rows)
				assert.Empty(t, h.provider.calls())
			})
		})
	}
}

func TestServiceEmptyRangeStillValidatesCatalog(t *testing.T) {
	cases := []struct {
		name   string
		symbol string
		want   error
	}{
		{"known", "BTCUSDT", nil}, {"unknown", "MISSING", application.ErrSymbolNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				h.query.Symbol, h.query.From = tc.symbol, h.query.To

				rows, err := h.service.Get(t.Context(), h.query)

				assert.ErrorIs(t, err, tc.want)
				assert.Empty(t, rows)
				assert.Empty(t, h.provider.calls())
			})
		})
	}
}

// Repository failures must stay failures, even if an upstream page was valid.
type failingRepository struct {
	kline.Repository
	getError, saveError error
}

func (r failingRepository) GetRange(ctx context.Context, q kline.Query) ([]kline.Stored, error) {
	if r.getError != nil {
		return nil, r.getError
	}
	return r.Repository.GetRange(ctx, q)
}

func (r failingRepository) UpsertMany(ctx context.Context, rows []kline.Stored) error {
	if r.saveError != nil {
		return r.saveError
	}
	return r.Repository.UpsertMany(ctx, rows)
}

func TestServiceRepositoryFailures(t *testing.T) {
	cases := []struct {
		name string
		read bool
	}{{"read", true}, {"save", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, serviceSettings(), 10)
				failure := errors.New("storage unavailable")
				repo := failingRepository{Repository: h.repository}
				if tc.read {
					repo.getError = failure
				} else {
					repo.saveError = failure
				}
				service, err := kline.NewService(t.Context(), repo, h.catalog, testOperations{12}, []kline.ScopeSettings{{Scope: h.query.Scope, Provider: h.provider, PageLimit: 10}}, serviceSettings(), time.Now, nil)
				require.NoError(t, err)

				rows, err := service.Get(t.Context(), h.query)

				assert.ErrorIs(t, err, failure)
				assert.Nil(t, rows)
				stored, err := h.repository.GetRange(t.Context(), h.query)
				require.NoError(t, err)
				assert.Empty(t, stored)
			})
		})
	}
}

func TestServiceDoesNotShareRefreshEvidenceInheritedFromEarlierFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, serviceSettings(), 10)
		open := h.query
		open.From, open.To = h.query.To, h.query.To.Add(time.Minute)
		wide := h.query
		wide.To = open.To
		firstRelease := make(chan struct{})
		secondRelease := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			release := firstRelease
			if request.From.Before(open.From) {
				release = secondRelease
			}
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		first := startGet(t.Context(), h.service, open)
		synctest.Wait()
		second := startGet(t.Context(), h.service, wide)
		synctest.Wait()
		close(firstRelease)
		require.NoError(t, (<-first).err)
		synctest.Wait()
		third := startGet(t.Context(), h.service, open)
		synctest.Wait()

		close(secondRelease)

		require.NoError(t, (<-second).err)
		got := <-third
		require.NoError(t, got.err)
		assert.Len(t, got.rows, 1)
		calls := h.provider.calls()
		require.Len(t, calls, 3)
		assert.Equal(t, open, calls[2].Query, "new caller must refresh the open slot")
	})
}

func TestServiceCanceledWaiterChurnDoesNotUseUpCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.MaxCallers = 2
		h := newHarness(t, settings, 10)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		leader := startGet(t.Context(), h.service, h.query)
		synctest.Wait()
		for range 100 {
			ctx, cancel := context.WithCancel(t.Context())
			waiter := startGet(ctx, h.service, h.query)
			synctest.Wait()
			cancel()
			assert.ErrorIs(t, (<-waiter).err, context.Canceled)
		}

		close(release)

		got := <-leader
		require.NoError(t, got.err)
		assert.Len(t, got.rows, 4)
		assert.Len(t, h.provider.calls(), 1)
		assert.Equal(t, uint64(100), h.metrics.Snapshot()[h.query.Scope].SharedWaits)
	})
}

func TestServiceMonthlyWindowUsesCalendarSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.HistoryCandles = 2
		h := newHarness(t, settings, 1)
		h.query.Interval = domain.Timeframe1M
		h.query.From = h.query.To.AddDate(0, -2, 0)

		rows, err := h.service.Get(t.Context(), h.query)

		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, time.Date(1999, 11, 1, 0, 0, 0, 0, time.UTC), rows[0].OpenTime)
		assert.Equal(t, time.Date(1999, 12, 1, 0, 0, 0, 0, time.UTC), rows[1].OpenTime)
		assert.Len(t, h.provider.calls(), 2)
	})
}

func TestServiceRejectsCallerBudgetExhaustionEvenIfSharedFillCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := serviceSettings()
		settings.MaxAttempts = 2
		h := newHarness(t, settings, 1)
		release := make(chan struct{})
		h.provider.fetch = func(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
			select {
			case <-release:
				return pageRows(request, time.Now()), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		first := h.query
		first.To = first.From.Add(time.Minute)
		second := h.query
		second.From, second.To = first.To, first.To.Add(2*time.Minute)
		leader := startGet(t.Context(), h.service, first)
		synctest.Wait()
		follower := startGet(t.Context(), h.service, second)
		synctest.Wait()

		close(release)

		require.NoError(t, (<-leader).err)
		got := <-follower
		assert.ErrorIs(t, got.err, application.ErrUpstreamAttemptLimit)
		assert.Nil(t, got.rows)
		synctest.Wait()
		stored, err := h.repository.GetRange(t.Context(), second)
		require.NoError(t, err)
		assert.Len(t, stored, 2, "the bounded owned fill may still finish")
		assert.Len(t, h.provider.calls(), 3)
	})
}
