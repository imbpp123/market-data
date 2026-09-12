package application

import (
	"unicode"
	"unicode/utf8"

	"market-data/internal/domain"
)

type SnapshotQuery struct {
	Exchange string
	Market   string
	Symbol   string
}

func SelectSnapshot(scopes []Scope, query SnapshotQuery) (SnapshotFilter, error) {
	filter := SnapshotFilter{}
	if query.Exchange != "" && !domain.Exchange(query.Exchange).Valid() {
		return filter, ErrInvalidFilter
	}

	if query.Market != "" && !domain.Market(query.Market).Valid() {
		return filter, ErrInvalidFilter
	}

	for _, scope := range scopes {
		if (query.Exchange == "" || query.Exchange == string(scope.Exchange)) && (query.Market == "" || query.Market == string(scope.Market)) {
			filter.Scopes = append(filter.Scopes, scope)
		}
	}

	if len(filter.Scopes) == 0 {
		return filter, ErrInvalidFilter
	}

	if query.Symbol != "" {
		if len(query.Symbol) > 128 || !utf8.ValidString(query.Symbol) {
			return filter, ErrInvalidFilter
		}

		for _, r := range query.Symbol {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return filter, ErrInvalidFilter
			}
		}

		filter.Symbol = &query.Symbol
	}

	return filter, nil
}
