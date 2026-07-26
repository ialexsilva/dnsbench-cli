package rank

import (
	"sort"

	"dnsbench/internal/model"
)

const tieEpsilonMs = 0.01

const (
	penaltyLoss           = "loss"
	penaltyServfail       = "servfail"
	penaltyInvalid        = "invalid-response"
	penaltyRetry          = "retry"
	penaltyJitter         = "jitter"
	penaltyNXInterception = "nxdomain-interception"
	penaltyNoDNSSEC       = "no-dnssec"
)

func ScoreServers(stats map[string]*model.ServerStats, probes map[string]*model.ProbeResult, categories []model.Category, w model.Weights, mode model.RankMode) []model.Score {
	scores := make([]model.Score, 0, len(stats))
	for id, st := range stats {
		if st == nil || st.State != model.StateActive {
			continue
		}
		cats, complete := rankableCategories(st, categories)
		if !complete {
			continue
		}
		base := weightedBase(st, cats, w)
		pens := computePenalties(st, cats, probes[id], w)
		total := base
		for _, v := range pens {
			total += v
		}
		scores = append(scores, model.Score{
			ServerID:  id,
			Mode:      mode,
			BaseMs:    base,
			Penalties: pens,
			TotalMs:   total,
		})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].TotalMs != scores[j].TotalMs {
			return scores[i].TotalMs < scores[j].TotalMs
		}
		return scores[i].ServerID < scores[j].ServerID
	})
	assignRanks(scores)
	return scores
}

func rankableCategories(st *model.ServerStats, configured []model.Category) ([]model.Category, bool) {
	if len(configured) == 0 {
		return nil, false
	}
	cats := make([]model.Category, 0, len(configured))
	seen := make(map[model.Category]bool, len(configured))
	for _, c := range configured {
		if seen[c] {
			continue
		}
		seen[c] = true
		d, ok := st.PerCategory[c]
		if !ok || d == nil || d.Count == 0 || d.Valid == 0 {
			return nil, false
		}
		cats = append(cats, c)
	}
	return cats, len(cats) > 0
}

func weightedBase(st *model.ServerStats, cats []model.Category, w model.Weights) float64 {
	weightSum := 0.0
	for _, c := range cats {
		weightSum += w.Category[c]
	}
	base := 0.0
	if weightSum <= 0 {
		for _, c := range cats {
			base += latencyMs(st.PerCategory[c], w.LatencyMetric)
		}
		return base / float64(len(cats))
	}
	for _, c := range cats {
		base += w.Category[c] / weightSum * latencyMs(st.PerCategory[c], w.LatencyMetric)
	}
	return base
}

func latencyMs(d *model.Distribution, metric string) float64 {
	switch metric {
	case "mean":
		return d.MeanMs
	case "p95":
		return d.P95Ms
	}
	return d.MedianMs
}

func computePenalties(st *model.ServerStats, cats []model.Category, probe *model.ProbeResult, w model.Weights) map[string]float64 {
	pens := map[string]float64{}
	totalQueries := 0
	unanswered := 0
	servfails := 0
	invalid := 0
	retried := 0
	jitterSum := 0.0
	for _, c := range cats {
		d := st.PerCategory[c]
		totalQueries += d.Count
		unanswered += d.Count - d.Answered
		servfails += d.Servfails
		invalid += d.Invalid
		retried += d.Retried
		jitterSum += d.JitterMs
	}
	if totalQueries > 0 {
		addPenalty(pens, penaltyLoss, w.PenaltyPerLossPctMs*float64(unanswered)/float64(totalQueries)*100)
		addPenalty(pens, penaltyServfail, w.PenaltyPerServfailPctMs*float64(servfails)/float64(totalQueries)*100)
		addPenalty(pens, penaltyInvalid, w.PenaltyPerInvalidPctMs*float64(invalid)/float64(totalQueries)*100)
		addPenalty(pens, penaltyRetry, w.PenaltyPerRetryPctMs*float64(retried)/float64(totalQueries)*100)
	}
	addPenalty(pens, penaltyJitter, w.JitterWeight*jitterSum/float64(len(cats)))
	if probe != nil {
		if probe.NXInterception == model.VerdictYes {
			addPenalty(pens, penaltyNXInterception, w.PenaltyNXInterceptionMs)
		}
		if probe.DNSSEC.Validating != model.VerdictYes {
			addPenalty(pens, penaltyNoDNSSEC, w.PenaltyNoDNSSECMs)
		}
	}
	if len(pens) == 0 {
		return nil
	}
	return pens
}

func addPenalty(pens map[string]float64, key string, value float64) {
	if value > 0 {
		pens[key] = value
	}
}

func assignRanks(scores []model.Score) {
	for i := range scores {
		if i > 0 && scores[i].TotalMs-scores[i-1].TotalMs < tieEpsilonMs {
			scores[i].Rank = scores[i-1].Rank
		} else {
			scores[i].Rank = i + 1
		}
	}
}
