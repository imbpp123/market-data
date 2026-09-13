package upstream

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"market-data/internal/application"
)

func (t *Transport) classify(request *http.Request, response *http.Response, body []byte, attempt *entry) (int64, error, bool) {
	var code int64
	if t.scope == Bybit {
		var envelope struct {
			Code *int64 `json:"retCode"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return 0, application.ErrInvalidUpstreamData, false
			}
		} else {
			code = *envelope.Code
		}
	}
	limited := t.rateSignal(request, response, body, code)
	if limited {
		return code, application.ErrUpstream, true
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || code != 0 {
		return code, application.ErrUpstream, retryableStatus(response.StatusCode)
	}
	if t.scope != Bybit && strings.HasSuffix(request.URL.Path, "/exchangeInfo") {
		if err := t.controller.updateCatalog(t.scope, attempt.at, body); err != nil {
			return code, err, false
		}
		// A catalog can install a window reported by this same response.
		t.controller.usageHeaders(t.scope, request.URL.Path, response.Header, attempt)
	}
	if !json.Valid(body) {
		return code, application.ErrInvalidUpstreamData, false
	}
	return code, nil, false
}

// Safety headers apply even when the status or response body is invalid.
func (t *Transport) responseHeaders(request *http.Request, response *http.Response, attempt *entry) {
	if t.scope != Bybit {
		t.controller.usageHeaders(t.scope, request.URL.Path, response.Header, attempt)
	}

	if retryableStatus(response.StatusCode) {
		if until, valid := retryAfter(response.Header.Get("Retry-After"), t.clock.Now()); valid {
			t.controller.Cooldown(t.scope, until)
		}
	}
}

func retryableStatus(status int) bool {
	return status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryAfter(value string, now time.Time) (time.Time, bool) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
		until := now.Add(time.Duration(seconds) * time.Second)
		return until, until.After(now)
	}
	until, err := http.ParseTime(value)
	return until, err == nil && until.After(now)
}

func (t *Transport) rateSignal(request *http.Request, response *http.Response, body []byte, code int64) bool {
	now := t.clock.Now()
	settings := t.controller.settings.Cooldown
	fallback := time.Duration(0)
	minimum := time.Duration(0)
	if t.scope == Bybit {
		switch {
		case response.StatusCode == 403 && accessTooFrequent(body):
			fallback = settings.Bybit403AccessTooFrequent
			minimum = 10 * time.Minute
		case response.StatusCode == 429:
			fallback = settings.Bybit429
		case code == 10006:
			fallback = settings.Bybit10006
		}
	} else {
		switch response.StatusCode {
		case 429:
			fallback = settings.Binance429
		case 418:
			fallback = settings.Binance418
		}
	}
	if fallback == 0 {
		return false
	}
	until, valid := retryAfter(response.Header.Get("Retry-After"), now)
	if t.scope == Bybit {
		if milliseconds, err := strconv.ParseInt(response.Header.Get("X-Bapi-Limit-Reset-Timestamp"), 10, 64); err == nil && milliseconds > 0 {
			reset := time.UnixMilli(milliseconds)
			if reset.After(now) {
				until = maxTime(until, reset)
				valid = true
			}
		}
	}
	if !valid {
		until = now.Add(fallback)
	}
	until = maxTime(until, now.Add(minimum))
	t.controller.Cooldown(t.scope, until)
	return true
}

func accessTooFrequent(body []byte) bool {
	var envelope struct {
		Message string `json:"retMsg"`
	}
	message := string(body)
	if json.Unmarshal(body, &envelope) == nil {
		message = envelope.Message
	}
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "access too frequent")
}

func windowSuffix(duration time.Duration) string {
	for _, unit := range []struct {
		size   time.Duration
		suffix string
	}{{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if duration%unit.size == 0 {
			return strconv.FormatInt(int64(duration/unit.size), 10) + unit.suffix
		}
	}
	return ""
}
