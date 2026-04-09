package keeper_test

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
	"go.uber.org/mock/gomock"
)

// ---- EpochHooks ----

func (s *KeeperTestSuite) TestAfterEpochEnd_WrongIdentifier() {
	// The module listens to "hour" epochs by default.
	// Firing "day" should be a no-op (no staking or bank calls).
	hooks := s.demurrageKeeper.Hooks()
	err := hooks.AfterEpochEnd(s.ctx, "day", 1)
	s.Require().NoError(err)

	// State should be unchanged.
	state, err := s.demurrageKeeper.State.Get(s.ctx)
	s.Require().NoError(err)
	s.True(state.GlobalAccumulator.Equal(math.LegacyOneDec()))
}

func (s *KeeperTestSuite) TestAfterEpochEnd_CorrectIdentifier() {
	// Firing "hour" epoch should call BondedRatio exactly once — UpdateDemurrageState
	// returns the ratio so AfterEpochEnd does not need to fetch it again for the event.
	bondingRatio := math.LegacyNewDecWithPrec(67, 2) // at goal — no rate change
	s.stakingKeeper.EXPECT().BondedRatio(gomock.Any()).Return(bondingRatio, nil).Times(1)

	hooks := s.demurrageKeeper.Hooks()
	err := hooks.AfterEpochEnd(s.ctx, "hour", 1)
	s.Require().NoError(err)

	state, err := s.demurrageKeeper.State.Get(s.ctx)
	s.Require().NoError(err)
	// Global accumulator must have decayed below 1.0.
	s.True(state.GlobalAccumulator.LT(math.LegacyOneDec()),
		"global accumulator must decay after one epoch, got %s", state.GlobalAccumulator)
}

func (s *KeeperTestSuite) TestAfterEpochEnd_AccumulatorDecaysMonotonically() {
	// After two consecutive epochs the accumulator must be strictly smaller.
	bondingRatio := math.LegacyNewDecWithPrec(50, 2) // below goal, rate rises
	s.stakingKeeper.EXPECT().BondedRatio(gomock.Any()).Return(bondingRatio, nil).AnyTimes()

	hooks := s.demurrageKeeper.Hooks()
	s.Require().NoError(hooks.AfterEpochEnd(s.ctx, "hour", 1))
	state1, err := s.demurrageKeeper.State.Get(s.ctx)
	s.Require().NoError(err)
	acc1 := state1.GlobalAccumulator

	s.Require().NoError(hooks.AfterEpochEnd(s.ctx, "hour", 2))
	state2, err := s.demurrageKeeper.State.Get(s.ctx)
	s.Require().NoError(err)
	acc2 := state2.GlobalAccumulator

	s.True(acc2.LT(acc1), "accumulator should decrease monotonically: %s >= %s", acc2, acc1)
}

// ---- StakingHooks ----
//
// All delegation-related hooks call MaterializeBalance, not a bare RefAcc reset.
// This prevents the re-delegation loophole: an actor cannot fast-forward RefAcc to
// GlobalAcc without first having accumulated levy collected.

// TestAfterDelegationModified_MaterializesBeforeReset verifies that when a delegation
// hook fires with a stale RefAcc and a zero liquid balance, the account state is GC'd
// (zero-balance fast path) and GetAccountState subsequently defaults to the current
// global — i.e. RefAcc correctly equals GlobalAcc without any retroactive levy.
func (s *KeeperTestSuite) TestAfterDelegationModified_MaterializesBeforeReset() {
	delAddr := makeAddr(0xAA)
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(88, 2))

	// Stale RefAcc — would create a levy if the account had non-zero balance.
	s.Require().NoError(
		s.demurrageKeeper.SetAccountState(s.ctx, delAddr, types.AccountDemurrageState{
			ReferenceAccumulator: math.LegacyNewDecWithPrec(95, 2),
			ReferenceBlock:       1,
		}),
	)

	// Zero balance: MaterializeBalance GCs the account state (AccountState.Remove).
	// No SendCoinsFromAccountToModule call is expected.
	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), delAddr, bondDenom).
		Return(sdk.NewCoin(bondDenom, math.ZeroInt()))

	hooks := s.demurrageKeeper.Hooks()
	err := hooks.AfterDelegationModified(s.ctx, delAddr, sdk.ValAddress(makeAddr(0x01)))
	s.Require().NoError(err)

	// After GC, GetAccountState returns the default state: RefAcc = current global (0.88).
	accountState, err := s.demurrageKeeper.GetAccountState(s.ctx, delAddr)
	s.Require().NoError(err)
	s.True(accountState.ReferenceAccumulator.Equal(math.LegacyNewDecWithPrec(88, 2)),
		"reference accumulator must equal global after delegation hook, got %s",
		accountState.ReferenceAccumulator)
}

// TestDelegationHook_LevyCollectedBeforeReset verifies that when a delegation hook
// fires with accumulated demurrage on a non-zero liquid balance, the levy is collected
// before RefAcc advances — not silently dropped.
func (s *KeeperTestSuite) TestDelegationHook_LevyCollectedBeforeReset() {
	delAddr := makeAddr(0xAB)
	s.setRefAccumulator(delAddr, math.LegacyOneDec()) // last touched at genesis (GlobalAcc = 1.0)
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(90, 2)) // decayed to 0.9 → 10% levy due

	stored := math.NewInt(1_000_000)
	levy := math.NewInt(100_000) // 1_000_000 × (1 - 0.9/1.0)
	levyCoins := sdk.NewCoins(sdk.NewCoin(bondDenom, levy))

	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), delAddr, bondDenom).
		Return(sdk.NewCoin(bondDenom, stored))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), delAddr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, stored))) // fully spendable
	s.bankKeeper.EXPECT().
		SendCoinsFromAccountToModule(gomock.Any(), delAddr, types.ModuleName, levyCoins).
		Return(nil)
	s.bankKeeper.EXPECT().
		SendCoinsFromModuleToModule(gomock.Any(), types.ModuleName, authtypes.FeeCollectorName, levyCoins).
		Return(nil)

	hooks := s.demurrageKeeper.Hooks()
	// Hook choice is arbitrary here; all four delegation hooks call MaterializeBalance.
	err := hooks.BeforeDelegationSharesModified(s.ctx, delAddr, sdk.ValAddress(makeAddr(0x01)))
	s.Require().NoError(err)

	// Levy was collected; RefAcc must now equal global.
	accountState, err := s.demurrageKeeper.GetAccountState(s.ctx, delAddr)
	s.Require().NoError(err)
	s.True(accountState.ReferenceAccumulator.Equal(math.LegacyNewDecWithPrec(90, 2)),
		"RefAcc must equal GlobalAcc after materialization via delegation hook, got %s",
		accountState.ReferenceAccumulator)
}
