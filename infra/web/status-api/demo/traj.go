package demo

// Entity positions in a snapshot are trajectories: a base, a velocity and a
// start time, evaluated at the time of the frame (BG_EvaluateTrajectory).  A
// thrown grenade or a dropped pack follows gravity until it lands.  The numbers
// are trType_t from q_shared.h.
const (
	trStationary     = 0
	trInterpolate    = 1
	trLinear         = 2
	trLinearStop     = 3
	trLinearStopBack = 4
	trSine           = 5
	trGravity        = 6
	trGravityLow     = 7
	trGravityFloat   = 8
	trGravityPaused  = 9

	defaultGravity = 800
)

// evalTrajectory is where a trajectory is at server time at.
func evalTrajectory(typ int, base, delta [3]float32, trTime, duration, at int32) [3]float32 {
	dt := float32(at-trTime) / 1000
	out := base
	switch typ {
	case trLinear, trLinearStopBack:
		for i := range out {
			out[i] += delta[i] * dt
		}
	case trLinearStop:
		if dt*1000 > float32(duration) {
			dt = float32(duration) / 1000
		}
		if dt < 0 {
			dt = 0
		}
		for i := range out {
			out[i] += delta[i] * dt
		}
	case trGravity, trGravityLow, trGravityFloat:
		for i := range out {
			out[i] += delta[i] * dt
		}
		switch typ {
		case trGravity:
			out[2] -= 0.5 * defaultGravity * dt * dt
		case trGravityLow:
			out[2] -= 0.5 * defaultGravity * 0.3 * dt * dt
		default:
			out[2] -= 0.5 * defaultGravity * 0.2 * dt
		}
	}
	return out // stationary, interpolated, paused: where it is
}
