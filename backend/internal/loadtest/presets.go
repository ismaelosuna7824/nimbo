package loadtest

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// Presets returns the built-in traffic shapes. Stages with DurationSec 0 are
// instant jumps, so "hold N rps" is a jump followed by a flat stage.
func Presets() []model.LoadProfile {
	hold := func(sec, rps int) []model.LoadStage {
		return []model.LoadStage{{DurationSec: 0, Target: rps}, {DurationSec: sec, Target: rps}}
	}
	var stress []model.LoadStage
	for _, rps := range []int{25, 50, 100, 200, 400} {
		stress = append(stress, hold(30, rps)...)
	}
	spike := append(hold(20, 10), model.LoadStage{DurationSec: 10, Target: 300})
	spike = append(spike, hold(20, 10)...)
	return []model.LoadProfile{
		{
			Preset:      "smoke",
			Label:       "Smoke test",
			Description: "A gentle 10-second check at 5 requests per second to confirm everything responds.",
			Mode:        model.ModeRPS,
			Stages:      hold(10, 5),
		},
		{
			Preset:      "load",
			Label:       "Load test",
			Description: "Ramps up to 50 requests per second, holds it for a minute, then winds down, like a typical busy period.",
			Mode:        model.ModeRPS,
			Stages: []model.LoadStage{
				{DurationSec: 30, Target: 50},
				{DurationSec: 60, Target: 50},
				{DurationSec: 10, Target: 0},
			},
		},
		{
			Preset:      "stress",
			Label:       "Stress test",
			Description: "Doubles the traffic every 30 seconds, from 25 up to 400 requests per second, to find where things start to break.",
			Mode:        model.ModeRPS,
			Stages:      stress,
		},
		{
			Preset:      "spike",
			Label:       "Spike test",
			Description: "Calm traffic, a sudden surge to 300 requests per second, then calm again, to check that the system recovers.",
			Mode:        model.ModeRPS,
			Stages:      spike,
		},
		{
			Preset:      "soak",
			Label:       "Soak test",
			Description: "A steady 20 requests per second for 10 minutes to reveal slow leaks and gradual slowdowns.",
			Mode:        model.ModeRPS,
			Stages:      hold(600, 20),
		},
	}
}
