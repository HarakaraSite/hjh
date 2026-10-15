package contextbudget

import (
	"context"
	"errors"

	"hjh/internal/contracts"
)

type Measurement struct {
	WireBytes       int64 `json:"wireBytes"`
	EstimatedTokens int64 `json:"estimatedTokens"`
	MessageCount    int   `json:"messageCount"`
	Fits            bool  `json:"fits"`
}

type Preview struct {
	Candidate  Measurement
	Projection Projection
}

// Preview uses the real selection algorithm on a detached projector. Candidate
// measures the largest considered input (including full raw current messages),
// not an exhaustive scan of older history beyond the first overflowing turn.
func (p *Projector) Preview(ctx context.Context, budget Budget, request contracts.ModelRequest,
	current []Entry, prepare func(contracts.ModelRequest) ([]byte, error)) (Preview, error) {
	if prepare == nil {
		return Preview{}, errors.New("context budget projection requires an adapter prepare function")
	}
	copy := *p
	copy.budget = cloneBudget(budget)
	copy.prefix = append([]Entry(nil), p.prefix...)
	copy.omittedCurrentIndices = make(map[int]struct{}, len(p.omittedCurrentIndices))
	for index := range p.omittedCurrentIndices {
		copy.omittedCurrentIndices[index] = struct{}{}
	}
	var candidate Measurement
	copy.candidateObserver = func(value evaluation) {
		if value.estimatedTokens >= candidate.EstimatedTokens {
			candidate = Measurement{WireBytes: value.wireBytes, EstimatedTokens: value.estimatedTokens,
				MessageCount: len(value.request.Messages), Fits: value.fits(budget)}
		}
	}
	if len(p.omittedCurrentIndices) != 0 {
		if _, err := copy.evaluate(request, current, allIndices(len(current)), nil, prepare); err != nil {
			return Preview{}, err
		}
	}
	projection, err := copy.Project(ctx, request, current, prepare)
	return Preview{Candidate: candidate, Projection: projection}, err
}

func (p Preview) Selected() Measurement {
	fact := p.Projection.Fact
	fits := fact.Budget.Limit == nil || fact.EstimatedTokens <= *fact.Budget.Limit
	if fact.Budget.HistoryTokens != nil && fact.HistoryEstimatedTokens != nil {
		fits = fits && *fact.HistoryEstimatedTokens <= *fact.Budget.HistoryTokens
	}
	return Measurement{WireBytes: p.Projection.Fact.WireBytes, EstimatedTokens: p.Projection.Fact.EstimatedTokens,
		MessageCount: len(p.Projection.Request.Messages), Fits: fits}
}

func (p Preview) Omitted() bool {
	fact := p.Projection.Fact
	return len(fact.OmittedCurrent) != 0 || fact.HistoryStatus == HistoryPartial || fact.HistoryStatus == HistoryOmitted
}
