package agent

import "math"

// ── Feature extraction ────────────────────────────────────────────────
//
// A single relative deviation ("signal", decision.go) can say "how far
// from normal right now." It cannot say "is this a spike or a slide,"
// "is this flapping or climbing," or "has this been going on for one
// tick or ten." Stage B classification (pattern.go) needs SHAPE, not
// just magnitude — FeatureVector is where raw History becomes shape.
//
// A deliberate departure from a literal reading of "extract from
// Observation.Value": every feature here is computed on the
// BASELINE-RELATIVE signal — the same relativeSignal() decision.go
// already uses for DangerScore — not on raw sensor units. Two reasons:
//
//  1. Consistency. decision.go already decided "surprise is relative,
//     not absolute" (a deviation of 5 means something different at
//     baseline=1000 than at baseline=10). Extracting features from raw
//     Value would silently reintroduce the exact problem that line was
//     written to avoid, one file over.
//  2. Threshold reuse. Because features live on the same relative scale
//     as DangerScore, pattern.go's templates can compare MaxDeviation
//     directly against watchThreshold/alertThreshold (decision.go's
//     existing, already-tuned constants) instead of inventing a second,
//     differently-scaled set of magnitude cutoffs.
//
// The tradeoff this creates, named rather than hidden: features are
// computed against a SINGLE baseline value (whatever State.Baseline was
// on the tick FeatureVector is extracted), even though baseline itself
// drifts continuously. Over a 10-tick window at typical baselineAlpha
// (0.10), that drift is small enough to ignore for shape detection —
// but it is an approximation, not exact history replay, because
// Observation itself (state.go) deliberately stores no per-tick
// baseline snapshot.
const featureWindow = 10

// stableEpsilon is how small |Delta| must be for a tick to count as
// "flat" (IsStable). Chosen well below watchThreshold: a plateau isn't
// just "not currently rising or falling fast," it's "barely moving at
// all" — a value could be rising fairly slowly and still fail IsStable,
// which is the intended split between GradualDrift and Plateau.
const stableEpsilon = 0.02

// FeatureVector summarizes the SHAPE of the last featureWindow
// baseline-relative readings — the raw material Stage B's
// PatternTemplates match against.
type FeatureVector struct {
	Delta         float64 // change from the previous reading, relative scale
	Slope         float64 // least-squares slope of the relative signal over the window
	Variance      float64 // rolling standard deviation of the relative signal
	Duration      int     // consecutive TRAILING ticks with |signal| > watchThreshold
	ZeroCrossings int     // how many times the relative signal changed sign within the window
	MaxDeviation  float64 // peak |signal| within the window
	IsRising      bool
	IsFalling     bool
	IsStable      bool
}

// ExtractFeatures computes a FeatureVector from up to the last
// featureWindow entries of history, relative to a single baseline.
// Safe to call with fewer than featureWindow observations (early in an
// agent's life): every statistic degrades gracefully to its natural
// zero-data value rather than panicking or dividing by zero.
func ExtractFeatures(history []Observation, baseline float64) FeatureVector {
	n := len(history)
	if n > featureWindow {
		history = history[n-featureWindow:]
		n = featureWindow
	}
	if n == 0 {
		return FeatureVector{IsStable: true}
	}

	signal := make([]float64, n)
	for i, obs := range history {
		signal[i] = relativeSignalSigned(obs.Value, baseline)
	}

	fv := FeatureVector{}

	// Delta: change from the previous reading (0 if this is the only
	// sample we have).
	if n >= 2 {
		fv.Delta = signal[n-1] - signal[n-2]
	}

	// Slope: ordinary least-squares fit of signal against tick index
	// 0..n-1. n<2 has no defined slope; leave it at the zero value.
	if n >= 2 {
		meanX := float64(n-1) / 2
		var meanY float64
		for _, v := range signal {
			meanY += v
		}
		meanY /= float64(n)

		var num, den float64
		for i, v := range signal {
			dx := float64(i) - meanX
			num += dx * (v - meanY)
			den += dx * dx
		}
		if den != 0 {
			fv.Slope = num / den
		}
	}

	// Variance: rolling standard deviation of the window (population,
	// not sample — the window itself defines the whole population we
	// care about, there's no larger sample being estimated from it).
	{
		var mean float64
		for _, v := range signal {
			mean += v
		}
		mean /= float64(n)
		var sumSq float64
		for _, v := range signal {
			d := v - mean
			sumSq += d * d
		}
		fv.Variance = math.Sqrt(sumSq / float64(n))
	}

	// ZeroCrossings: sign changes of the relative signal within the
	// window — a cheap, deterministic proxy for "is this oscillating
	// around baseline" without needing a real frequency-domain
	// transform (which would also start smelling like ML machinery
	// this project's non-goals explicitly rule out).
	for i := 1; i < n; i++ {
		if (signal[i-1] > 0 && signal[i] < 0) || (signal[i-1] < 0 && signal[i] > 0) {
			fv.ZeroCrossings++
		}
	}

	// MaxDeviation: peak absolute relative deviation anywhere in the
	// window.
	for _, v := range signal {
		if math.Abs(v) > fv.MaxDeviation {
			fv.MaxDeviation = math.Abs(v)
		}
	}

	// Duration: consecutive TRAILING ticks (walking backward from the
	// most recent) whose |signal| exceeds watchThreshold. Stops at the
	// first tick that doesn't qualify — this is "how long has the
	// CURRENT abnormal streak lasted," not "how many abnormal ticks
	// exist anywhere in the window."
	for i := n - 1; i >= 0; i-- {
		if math.Abs(signal[i]) > watchThreshold {
			fv.Duration++
		} else {
			break
		}
	}

	fv.IsRising = fv.Delta > stableEpsilon && fv.Slope > 0
	fv.IsFalling = fv.Delta < -stableEpsilon && fv.Slope < 0
	fv.IsStable = math.Abs(fv.Delta) <= stableEpsilon

	return fv
}
