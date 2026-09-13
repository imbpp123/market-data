package upstream

import (
	"slices"
	"strings"
	"time"
)

// AdmissionSnapshot contains a read-only view at one controller clock instant.
// Operation checks use the largest configured request cost for that window.
// They exclude queue slots, operation deadlines, and request-specific backoff.
type AdmissionSnapshot struct {
	At     time.Time         `json:"at"`
	Scopes []ScopeDiagnostic `json:"scopes"`
}

type ScopeDiagnostic struct {
	Scope            Scope              `json:"scope"`
	CatalogUpdatedAt time.Time          `json:"catalog_updated_at"`
	CatalogError     string             `json:"catalog_error,omitempty"`
	CatalogErrorAt   time.Time          `json:"catalog_error_at"`
	CooldownUntil    time.Time          `json:"cooldown_until"`
	PacingUntil      time.Time          `json:"pacing_until"`
	Windows          []WindowDiagnostic `json:"windows"`
}

type WindowDiagnostic struct {
	Name              string                `json:"name"`
	Unit              string                `json:"unit"`
	WindowSeconds     float64               `json:"window_seconds"`
	Source            string                `json:"source"`
	UpdatedAt         time.Time             `json:"updated_at"`
	AgeSeconds        float64               `json:"age_seconds"`
	ExchangeLimit     int                   `json:"exchange_limit"`
	UserCap           int                   `json:"user_cap"`
	ThresholdPercent  int                   `json:"threshold_percent"`
	StopLine          int                   `json:"stop_line"`
	Observed          int                   `json:"observed"`
	Local             int                   `json:"local"`
	Accounted         int                   `json:"accounted"`
	Reserved          int                   `json:"reserved"`
	Remaining         int                   `json:"remaining"`
	Uncertain         bool                  `json:"uncertain"`
	IncompleteHistory bool                  `json:"incomplete_history"`
	HistorySince      time.Time             `json:"history_since"`
	Operations        []OperationDiagnostic `json:"operations"`
}

type OperationDiagnostic struct {
	Operation    string    `json:"operation"`
	RequestCost  int       `json:"request_cost"`
	Share        int       `json:"share"`
	Local        int       `json:"local"`
	Remaining    int       `json:"remaining"`
	Reasons      []string  `json:"reasons"`
	NextEligible time.Time `json:"next_eligible"`
}

func (c *Controller) Diagnostics() AdmissionSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	result := AdmissionSnapshot{At: now, Scopes: []ScopeDiagnostic{}}
	for _, scope := range []Scope{BinanceSpot, BinanceLinear} {
		s := c.scopes[scope]
		if s == nil {
			continue
		}
		d := ScopeDiagnostic{Scope: scope, CatalogUpdatedAt: s.catalogUpdatedAt, CatalogError: s.catalogError,
			CatalogErrorAt: s.catalogErrorAt, Windows: []WindowDiagnostic{}}
		if s.cooldown.After(now) {
			d.CooldownUntil = s.cooldown
		}
		if s.next.After(now) {
			d.PacingUntil = s.next
		}
		for name, w := range s.windows {
			u := s.usage(w, now)
			ageFrom := w.updatedAt
			if ageFrom.IsZero() {
				ageFrom = s.startedAt
			}
			view := WindowDiagnostic{Name: name, Unit: w.unit, WindowSeconds: w.duration.Seconds(), Source: w.source,
				UpdatedAt: w.updatedAt, AgeSeconds: max(0, now.Sub(ageFrom).Seconds()), ExchangeLimit: w.ceiling,
				UserCap: w.userCap, ThresholdPercent: c.settings.Binance.StopThresholdPercent, StopLine: w.limit,
				Observed: u.observed, Local: u.local, Accounted: u.common, Reserved: u.reserved,
				Remaining: max(0, w.limit-u.common), Uncertain: u.uncertain, IncompleteHistory: u.incompleteHistory,
				HistorySince: s.historySince, Operations: []OperationDiagnostic{}}
			for op := range operationCount {
				if w.funding && op != Instruments {
					continue
				}
				request := cost{operation: op, weight: s.maxCosts[op], funding: w.funding}
				view.Operations = append(view.Operations, s.operationDiagnostic(w, request, u, now))
			}
			d.Windows = append(d.Windows, view)
		}
		slices.SortFunc(d.Windows, func(a, b WindowDiagnostic) int { return strings.Compare(a.Name, b.Name) })
		result.Scopes = append(result.Scopes, d)
	}
	return result
}

func (s *scopeState) operationDiagnostic(w window, request cost, usage usageView, now time.Time) OperationDiagnostic {
	amount := w.cost(request)
	share := w.limit
	if w.split {
		share = percent(w.limit, s.shares[request.operation])
	}
	d := OperationDiagnostic{Operation: operationLabel(request.operation), RequestCost: amount, Share: share,
		Local: usage.operations[request.operation], Remaining: max(0, share-usage.operations[request.operation]),
		Reasons: []string{}, NextEligible: maxTime(now, s.next, s.cooldown)}
	impossible := amount > w.limit || amount > share
	common := amount > 0 && usage.common > w.limit
	operation := amount > 0 && w.split && usage.operations[request.operation] > share-amount
	if impossible {
		d.Reasons = append(d.Reasons, "request_cost_exceeds_allowance")
	}
	if common {
		d.Reasons = append(d.Reasons, "common_threshold")
	}
	if operation && !impossible {
		d.Reasons = append(d.Reasons, "operation_share")
	}
	if s.cooldown.After(now) {
		d.Reasons = append(d.Reasons, "exchange_cooldown")
	}
	if impossible {
		d.NextEligible = time.Time{}
	} else if common || operation {
		next := s.budgetReady(w, request, now)
		if next.IsZero() {
			d.NextEligible = time.Time{}
		} else {
			d.NextEligible = maxTime(d.NextEligible, next)
		}
	}
	return d
}

// DiagnosticEvent reports transitions separately from dispatched HTTP attempts.
// The observer runs under the controller lock and must not call the controller.
type DiagnosticEvent struct {
	RequestWeight    int
	FundingRequest   bool
	Limits           []LimitState
	ThresholdPercent int
	Scope            Scope
	Reason           string
	Operation        string
	NextEligible     time.Time
}

func (c *Controller) SetDiagnosticObserver(observe func(DiagnosticEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diagnosticObserver = observe
}

func (c *Controller) diagnostic(event DiagnosticEvent) {
	if c.diagnosticObserver != nil {
		c.diagnosticObserver(event)
	}
}

func (c *Controller) admissionDiagnostic(scope Scope, request cost, reason string, next time.Time) {
	s := c.scopes[scope]
	op := request.operation
	// Several windows can reject the same operation. Stay quiet until a
	// request is admitted, even if another blocked window supplies the reason.
	if (s.lastAdmission[op] != "") == (reason != "") {
		return
	}
	s.lastAdmission[op] = reason
	if reason == "" {
		reason = "request_admitted"
	}
	c.diagnostic(DiagnosticEvent{Scope: scope, Operation: operationLabel(op), Reason: reason, NextEligible: next, RequestWeight: request.weight, FundingRequest: request.funding})
}

// Catalog transport failures must not replace a newer accepted catalog state.
func (c *Controller) catalogFailure(scope Scope, started time.Time, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.scopes[scope]
	if started.Before(s.catalogStart) || started.Before(s.catalogErrorStart) {
		return
	}
	s.catalogError, s.catalogErrorAt, s.catalogErrorStart = reason, c.clock.Now(), started
	c.diagnostic(DiagnosticEvent{Scope: scope, Reason: reason})
}

func operationLabel(op Operation) string {
	switch op {
	case Tickers:
		return "tickers"
	case Klines:
		return "klines"
	case Instruments:
		return "instruments"
	case MarketStats:
		return "market_stats"
	default:
		return "unknown"
	}
}
