package agent

import (
	"math/rand"
	"time"
)

// SyntheticSensor produces noisy-but-stable readings around a mean, with
// occasional injected spikes. It exists purely so a single agent has
// something to react to before there's any real environment, network, or
// other agents to observe. It implements Sensor.
type SyntheticSensor struct {
	Mean      float64
	StdDev    float64
	SpikeProb float64 // chance per reading that a spike is injected
	SpikeMag  float64
	rng       *rand.Rand
}

func NewSyntheticSensor(mean, stddev, spikeProb, spikeMag float64) *SyntheticSensor {
	return &SyntheticSensor{
		Mean:      mean,
		StdDev:    stddev,
		SpikeProb: spikeProb,
		SpikeMag:  spikeMag,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SyntheticSensor) Read() float64 {
	v := s.Mean + s.rng.NormFloat64()*s.StdDev
	if s.rng.Float64() < s.SpikeProb {
		v += s.SpikeMag
	}
	return v
}
