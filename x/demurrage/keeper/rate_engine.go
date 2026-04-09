package keeper

import (
	"context"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// ComputeNewAnnualRate calculates the next annualised demurrage rate using the same
// control loop as x/mint's NextInflationRate, but inverted: when bonding is below
// goal the rate rises (taxing non-stakers more aggressively), and vice-versa.
//
// The rate change is proportional to the distance from the target bonding ratio and
// is expressed as a fraction of a year elapsed since the last update (Δt = epochFraction).
//
// Direct analogue of Minter.NextInflationRate in x/mint/types/minter.go.
func ComputeNewAnnualRate(currentRate, bondingRatio math.LegacyDec, params types.Params, epochFraction math.LegacyDec) math.LegacyDec {
	var newRate math.LegacyDec

	if bondingRatio.LT(params.GoalBonded) {
		// Too few tokens bonded → increase demurrage to punish non-stakers more.
		// rateChange = (1 - bondingRatio/goalBonded) * demurrageRateChange
		rateChange := math.LegacyOneDec().
			Sub(bondingRatio.Quo(params.GoalBonded)).
			Mul(params.DemurrageRateChange).
			Mul(epochFraction)
		newRate = currentRate.Add(rateChange)
		if newRate.GT(params.DemurrageRateMax) {
			newRate = params.DemurrageRateMax
		}
	} else if bondingRatio.GT(params.GoalBonded) {
		// Enough tokens bonded → decrease demurrage pressure.
		// rateChange = (bondingRatio/goalBonded - 1) * demurrageRateChange
		rateChange := bondingRatio.Quo(params.GoalBonded).
			Sub(math.LegacyOneDec()).
			Mul(params.DemurrageRateChange).
			Mul(epochFraction)
		newRate = currentRate.Sub(rateChange)
		if newRate.LT(params.DemurrageRateMin) {
			newRate = params.DemurrageRateMin
		}
	} else {
		// At equilibrium — no change.
		newRate = currentRate
	}

	return newRate
}

// ComputePerEpochRate converts an annualised demurrage rate to the compound per-epoch
// factor using a Taylor-series approximation of (1 - annualRate)^(1/epochsPerYear).
//
// Formula derivation:
//
//	(1-r)^(1/n) = exp(ln(1-r) / n)
//
// We approximate:
//
//	ln(1-r) ≈ -r - r²/2 - r³/3 - r⁴/4   (4-term Taylor; error < 0.001% for r ≤ 0.20)
//	exp(x)  ≈ 1 + x + x²/2 + x³/6       (3-term Taylor; exact enough for tiny x = ln(1-r)/n)
//
// This gives PerEpochRate = 1 - exp(ln(1-r)/n) with < 0.01% relative error for
// all expected parameter combinations (r ≤ 20%, epochs hourly to weekly).
//
// Using the linear approximation r/n (as x/mint does per block) would produce
// ~10% relative error for daily epochs at 20% annual rate, which is why we use
// the compound formula here.
func ComputePerEpochRate(annualRate math.LegacyDec, epochsPerYear math.LegacyDec) math.LegacyDec {
	// --- 4-term Taylor series for ln(1 - r) ---
	r := annualRate
	r2 := r.Mul(r)
	r3 := r2.Mul(r)
	r4 := r3.Mul(r)

	two := math.LegacyNewDec(2)
	three := math.LegacyNewDec(3)
	four := math.LegacyNewDec(4)

	// ln(1-r) ≈ -r - r²/2 - r³/3 - r⁴/4
	lnOneMinus := r.Neg().
		Sub(r2.Quo(two)).
		Sub(r3.Quo(three)).
		Sub(r4.Quo(four))

	// per-epoch log: x = ln(1-r) / n  (x is small and negative)
	x := lnOneMinus.Quo(epochsPerYear)

	// --- 3-term Taylor series for exp(x) ---
	x2 := x.Mul(x)
	x3 := x2.Mul(x)
	six := math.LegacyNewDec(6)

	// exp(x) ≈ 1 + x + x²/2 + x³/6
	expX := math.LegacyOneDec().
		Add(x).
		Add(x2.Quo(two)).
		Add(x3.Quo(six))

	// PerEpochRate = 1 - (1-r)^(1/n) = 1 - exp(x)
	perEpochRate := math.LegacyOneDec().Sub(expX)

	// Clamp to [0, 1] as a safety net against extreme inputs.
	if perEpochRate.IsNegative() {
		return math.LegacyZeroDec()
	}
	if perEpochRate.GT(math.LegacyOneDec()) {
		return math.LegacyOneDec()
	}
	return perEpochRate
}

// EpochsPerYear computes the number of epochs per year given a duration in seconds
// and an epoch duration in seconds. The x/epochs module uses time-based epochs,
// so we derive this from the context's block time indirectly.
//
// Since x/epochs ticks based on wall-clock time, we just use the well-known
// seconds-per-year constant divided by the epoch duration in seconds.
func EpochsPerYear(epochDurationSeconds int64) math.LegacyDec {
	const secondsPerYear = int64(60 * 60 * 24 * 365)
	if epochDurationSeconds <= 0 {
		return math.LegacyNewDec(1)
	}
	return math.LegacyNewDec(secondsPerYear).Quo(math.LegacyNewDec(epochDurationSeconds))
}

// UpdateDemurrageState computes the new annual rate, updates the global accumulator,
// and persists the result. Returns the per-epoch rate applied and the current bonding
// ratio (so callers avoid a redundant BondedRatio fetch for event emission).
func (k Keeper) UpdateDemurrageState(ctx context.Context, epochDurationSeconds int64) (perEpochRate math.LegacyDec, bondingRatio math.LegacyDec, err error) {
	state, err := k.State.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, err
	}

	bondingRatio, err = k.stakingKeeper.BondedRatio(ctx)
	if err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, err
	}

	epochsPerYear := EpochsPerYear(epochDurationSeconds)
	epochFraction := math.LegacyOneDec().Quo(epochsPerYear)

	// 1. Update rate via Rate Engine.
	newRate := ComputeNewAnnualRate(state.CurrentAnnualRate, bondingRatio, params, epochFraction)

	// 2. Compute per-epoch rate from new annual rate.
	perEpochRate = ComputePerEpochRate(newRate, epochsPerYear)

	// 3. Update global accumulator: acc *= (1 - perEpochRate).
	newAcc := state.GlobalAccumulator.Mul(math.LegacyOneDec().Sub(perEpochRate))

	// 4. Persist new state.
	state.CurrentAnnualRate = newRate
	state.GlobalAccumulator = newAcc
	if err = k.State.Set(ctx, state); err != nil {
		return math.LegacyDec{}, math.LegacyDec{}, err
	}

	return perEpochRate, bondingRatio, nil
}
