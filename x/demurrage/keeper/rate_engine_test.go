package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/x/demurrage/keeper"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// ---- EpochsPerYear ----

func TestEpochsPerYear(t *testing.T) {
	tests := []struct {
		name     string
		duration int64
		wantNear string // approximate expected value
	}{
		{"hour", 3600, "8760"},    // 365*24
		{"day", 86400, "365"},
		{"week", 7 * 86400, "52"}, // 365/7 ≈ 52.14
		{"minute", 60, "525600"},  // 365*24*60
		{"zero_falls_back", 0, "1"},
		{"negative_falls_back", -1, "1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := keeper.EpochsPerYear(tc.duration)
			want, ok := math.LegacyNewDecFromStr(tc.wantNear)
			require.True(t, ok == nil)
			// Allow ±1 unit of tolerance for the truncated int part.
			diff := got.Sub(want).Abs()
			require.True(t, diff.LTE(math.LegacyNewDec(1)),
				"EpochsPerYear(%d) = %s, want ~%s", tc.duration, got, tc.wantNear)
		})
	}
}

// ---- ComputePerEpochRate ----

// TestComputePerEpochRate_Hourly verifies that the per-epoch rate for a 10% annual rate
// and hourly epochs is close to the analytical value: 1 - (0.9)^(1/8760).
// Analytical ≈ 0.00001200682...
func TestComputePerEpochRate_Hourly(t *testing.T) {
	annual := math.LegacyNewDecWithPrec(10, 2) // 10%
	epochsPerYear := keeper.EpochsPerYear(3600)
	got := keeper.ComputePerEpochRate(annual, epochsPerYear)

	// Expected analytical value ≈ 1.201e-5
	lo, _ := math.LegacyNewDecFromStr("0.0000118")
	hi, _ := math.LegacyNewDecFromStr("0.0000122")
	require.True(t, got.GTE(lo) && got.LTE(hi),
		"ComputePerEpochRate(10%%, hourly) = %s, want in [%s, %s]", got, lo, hi)
}

// TestComputePerEpochRate_Daily20Pct verifies the daily rate at 20% annual is reasonable.
// Analytical: 1 - (0.8)^(1/365) ≈ 0.000611
func TestComputePerEpochRate_Daily20Pct(t *testing.T) {
	annual := math.LegacyNewDecWithPrec(20, 2) // 20%
	epochsPerYear := keeper.EpochsPerYear(86400)
	got := keeper.ComputePerEpochRate(annual, epochsPerYear)

	lo, _ := math.LegacyNewDecFromStr("0.000605")
	hi, _ := math.LegacyNewDecFromStr("0.000620")
	require.True(t, got.GTE(lo) && got.LTE(hi),
		"ComputePerEpochRate(20%%, daily) = %s, want in [%s, %s]", got, lo, hi)
}

// TestComputePerEpochRate_ZeroRate ensures zero annual rate returns zero per-epoch rate.
func TestComputePerEpochRate_ZeroRate(t *testing.T) {
	got := keeper.ComputePerEpochRate(math.LegacyZeroDec(), math.LegacyNewDec(8760))
	require.True(t, got.IsZero(), "zero annual rate must yield zero per-epoch rate, got %s", got)
}

// TestComputePerEpochRate_Clamped ensures extreme inputs don't produce negative or >1 rates.
func TestComputePerEpochRate_Clamped(t *testing.T) {
	// Very high rate (edge of valid range)
	got := keeper.ComputePerEpochRate(math.LegacyNewDecWithPrec(99, 2), math.LegacyOneDec())
	require.False(t, got.IsNegative(), "per-epoch rate must not be negative")
	require.False(t, got.GT(math.LegacyOneDec()), "per-epoch rate must not exceed 1")
}

// TestComputePerEpochRate_CompoundVsLinearError verifies that the compound per-epoch
// rate is strictly greater than the linear approximation (r/n), and that the relative
// difference is approximately r/2 ≈ 3.5% for r=7%.  This demonstrates why we use the
// compound formula: the linear r/n underestimates the per-epoch decay at annual rates.
func TestComputePerEpochRate_CompoundVsLinearError(t *testing.T) {
	annual := math.LegacyNewDecWithPrec(7, 2) // 7%
	epochsPerYear := keeper.EpochsPerYear(3600)

	compound := keeper.ComputePerEpochRate(annual, epochsPerYear)
	linear := annual.Quo(epochsPerYear)

	// Compound must always exceed linear: 1-(1-r)^(1/n) > r/n for r∈(0,1).
	require.True(t, compound.GT(linear),
		"compound per-epoch rate (%s) must exceed linear approximation (%s)", compound, linear)

	// Relative difference ≈ r/2 = 3.5% for r=7%.  Verify it is in [3%, 4%].
	relDiff := compound.Sub(linear).Quo(compound)
	lo, _ := math.LegacyNewDecFromStr("0.030")
	hi, _ := math.LegacyNewDecFromStr("0.040")
	require.True(t, relDiff.GTE(lo) && relDiff.LTE(hi),
		"compound-vs-linear relative diff should be ~3.5%%, got %s", relDiff)
}

// ---- ComputeNewAnnualRate ----

func TestComputeNewAnnualRate_BelowGoal(t *testing.T) {
	params := types.DefaultParams() // goal=67%, min=7%, max=20%, change=13%
	currentRate := math.LegacyNewDecWithPrec(10, 2)
	// Bonding at 50% — well below goal of 67%. Rate should increase.
	bondingRatio := math.LegacyNewDecWithPrec(50, 2)
	// epochFraction = 1/8760 (1 hour out of a year)
	epochFraction := math.LegacyOneDec().Quo(math.LegacyNewDec(8760))

	newRate := keeper.ComputeNewAnnualRate(currentRate, bondingRatio, params, epochFraction)
	require.True(t, newRate.GT(currentRate), "rate should rise when bonding < goal")
	require.True(t, newRate.LTE(params.DemurrageRateMax), "rate must not exceed max")
}

func TestComputeNewAnnualRate_AboveGoal(t *testing.T) {
	params := types.DefaultParams()
	currentRate := math.LegacyNewDecWithPrec(15, 2)
	// Bonding at 80% — above goal of 67%. Rate should decrease.
	bondingRatio := math.LegacyNewDecWithPrec(80, 2)
	epochFraction := math.LegacyOneDec().Quo(math.LegacyNewDec(8760))

	newRate := keeper.ComputeNewAnnualRate(currentRate, bondingRatio, params, epochFraction)
	require.True(t, newRate.LT(currentRate), "rate should fall when bonding > goal")
	require.True(t, newRate.GTE(params.DemurrageRateMin), "rate must not go below min")
}

func TestComputeNewAnnualRate_AtGoal(t *testing.T) {
	params := types.DefaultParams()
	currentRate := math.LegacyNewDecWithPrec(13, 2)
	bondingRatio := params.GoalBonded // exactly at goal
	epochFraction := math.LegacyOneDec().Quo(math.LegacyNewDec(8760))

	newRate := keeper.ComputeNewAnnualRate(currentRate, bondingRatio, params, epochFraction)
	require.True(t, newRate.Equal(currentRate), "rate should be unchanged at goal bonding")
}

func TestComputeNewAnnualRate_ClampsToMax(t *testing.T) {
	params := types.DefaultParams()
	// Start at max, bonding very low
	currentRate := params.DemurrageRateMax
	bondingRatio := math.LegacyZeroDec()
	epochFraction := math.LegacyOneDec() // one full year in one epoch

	newRate := keeper.ComputeNewAnnualRate(currentRate, bondingRatio, params, epochFraction)
	require.True(t, newRate.Equal(params.DemurrageRateMax), "rate must be clamped to max")
}

func TestComputeNewAnnualRate_ClampsToMin(t *testing.T) {
	params := types.DefaultParams()
	// Start at min, bonding very high
	currentRate := params.DemurrageRateMin
	bondingRatio := math.LegacyOneDec() // 100% bonded
	epochFraction := math.LegacyOneDec()

	newRate := keeper.ComputeNewAnnualRate(currentRate, bondingRatio, params, epochFraction)
	require.True(t, newRate.Equal(params.DemurrageRateMin), "rate must be clamped to min")
}
