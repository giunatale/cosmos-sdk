package keeper_test

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
	"go.uber.org/mock/gomock"
)

const bondDenom = "uatone"

// makeAddr creates a deterministic test address from a single byte.
func makeAddr(b byte) sdk.AccAddress {
	addr := make([]byte, 20)
	addr[0] = b
	return sdk.AccAddress(addr)
}

// setGlobalAccumulator is a convenience helper to write the global accumulator into the keeper store.
func (s *KeeperTestSuite) setGlobalAccumulator(acc math.LegacyDec) {
	state, err := s.demurrageKeeper.State.Get(s.ctx)
	s.Require().NoError(err)
	state.GlobalAccumulator = acc
	s.Require().NoError(s.demurrageKeeper.State.Set(s.ctx, state))
}

// setRefAccumulator stores the given reference accumulator for an account,
// simulating an account that was last touched when the global was at refAcc.
func (s *KeeperTestSuite) setRefAccumulator(addr sdk.AccAddress, refAcc math.LegacyDec) {
	s.Require().NoError(s.demurrageKeeper.SetAccountState(s.ctx, addr, types.AccountDemurrageState{
		ReferenceAccumulator: refAcc,
		ReferenceBlock:       s.ctx.BlockHeight(),
	}))
}

// ---- IsExempt ----

func (s *KeeperTestSuite) TestIsExempt_ModuleAccount() {
	// The demurrage module account itself must always be exempt.
	moduleAddr := sdk.AccAddress([]byte("demurrage_module_acct"))
	// accountKeeper.GetModuleAddress is already wired to return moduleAddr.
	s.True(s.demurrageKeeper.IsExempt(s.ctx, moduleAddr))
}

func (s *KeeperTestSuite) TestIsExempt_ExemptList() {
	// SetupTest wires GetModuleAddress for each exempt module account.
	// addr[0] == 0x01 corresponds to "bonded_tokens_pool" (index 0 → byte 1).
	exemptAddr := sdk.AccAddress(func() []byte { a := make([]byte, 20); a[0] = 0x01; return a }())
	s.True(s.demurrageKeeper.IsExempt(s.ctx, exemptAddr))
}

func (s *KeeperTestSuite) TestIsExempt_RegularAddress() {
	// makeAddr(0x99) does not match the module account or any exempt-list address.
	regularAddr := makeAddr(0x99)
	s.False(s.demurrageKeeper.IsExempt(s.ctx, regularAddr))
}

// ---- GetAccountState ----

func (s *KeeperTestSuite) TestGetAccountState_NewAccount() {
	// An account with no stored state should default to the current global accumulator.
	addr := makeAddr(0x01)
	state, err := s.demurrageKeeper.GetAccountState(s.ctx, addr)
	s.Require().NoError(err)
	s.True(state.ReferenceAccumulator.Equal(math.LegacyOneDec()),
		"new account's reference accumulator should equal global (1.0)")
}

func (s *KeeperTestSuite) TestGetAccountState_ExistingAccount() {
	addr := makeAddr(0x02)
	stored := types.AccountDemurrageState{
		ReferenceAccumulator: math.LegacyNewDecWithPrec(95, 2),
		ReferenceBlock:       5,
	}
	s.Require().NoError(s.demurrageKeeper.SetAccountState(s.ctx, addr, stored))

	got, err := s.demurrageKeeper.GetAccountState(s.ctx, addr)
	s.Require().NoError(err)
	s.True(got.ReferenceAccumulator.Equal(stored.ReferenceAccumulator))
}

// ---- EffectiveBalance ----

func (s *KeeperTestSuite) TestEffectiveBalance_ExemptAccount() {
	moduleAddr := sdk.AccAddress([]byte("demurrage_module_acct"))
	expectedAmt := math.NewInt(1_000_000)
	s.bankKeeper.EXPECT().
		GetBalance(s.ctx, moduleAddr, bondDenom).
		Return(sdk.NewCoin(bondDenom, expectedAmt))

	result, err := s.demurrageKeeper.EffectiveBalance(s.ctx, moduleAddr, bondDenom)
	s.Require().NoError(err)
	s.True(result.Equal(expectedAmt), "exempt accounts must not have demurrage applied")
}

func (s *KeeperTestSuite) TestEffectiveBalance_Decay() {
	// Global accumulator has decayed to 0.9: account should see 10% reduction.
	// Must set refAcc = 1.0 before decaying the global, otherwise GetAccountState
	// would default refAcc to the already-decayed global (0.9), yielding no decay.
	addr := makeAddr(0x20)
	s.setRefAccumulator(addr, math.LegacyOneDec()) // account was created at genesis
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(90, 2)) // 0.9

	stored := math.NewInt(1_000_000)
	// Effective = 1_000_000 * (0.9 / 1.0) = 900_000
	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, stored))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), addr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, stored))) // fully spendable

	result, err := s.demurrageKeeper.EffectiveBalance(s.ctx, addr, bondDenom)
	s.Require().NoError(err)
	s.True(result.Equal(math.NewInt(900_000)),
		"effective balance should be 900_000 (10%% decay), got %s", result)
}

func (s *KeeperTestSuite) TestEffectiveBalance_VestingPartial() {
	// Half the balance is locked (vesting), demurrage only on spendable half.
	addr := makeAddr(0x21)
	s.setRefAccumulator(addr, math.LegacyOneDec()) // account was created at genesis
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(90, 2)) // 0.9

	total := math.NewInt(1_000_000)
	spendable := math.NewInt(500_000)

	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, total))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), addr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, spendable)))

	// Expected: locked(500_000) + effective_spendable(500_000 * 0.9) = 500_000 + 450_000 = 950_000
	result, err := s.demurrageKeeper.EffectiveBalance(s.ctx, addr, bondDenom)
	s.Require().NoError(err)
	s.True(result.Equal(math.NewInt(950_000)),
		"vesting: locked+decayed_spendable = 950_000, got %s", result)
}

func (s *KeeperTestSuite) TestEffectiveBalance_ZeroBalance() {
	// For an exempt account with zero stored balance, EffectiveBalance returns zero
	// without calling BondDenom or SpendableCoins (exempt short-circuit).
	addr := makeAddr(0x05) // community_pool address — exempt
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, math.ZeroInt()))

	result, err := s.demurrageKeeper.EffectiveBalance(s.ctx, addr, bondDenom)
	s.Require().NoError(err)
	s.True(result.IsZero())
}

// ---- MaterializeBalance ----

func (s *KeeperTestSuite) TestMaterializeBalance_ExemptSkipped() {
	moduleAddr := sdk.AccAddress([]byte("demurrage_module_acct"))
	// No bank calls should happen for exempt accounts.
	err := s.demurrageKeeper.MaterializeBalance(s.ctx, moduleAddr)
	s.Require().NoError(err)
}

func (s *KeeperTestSuite) TestMaterializeBalance_LevyAndRedistribute() {
	addr := makeAddr(0x23)
	s.setRefAccumulator(addr, math.LegacyOneDec()) // account was created at genesis
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(90, 2)) // 0.9

	stored := math.NewInt(1_000_000)
	spendable := math.NewInt(1_000_000)
	// Effective = 1_000_000 * 0.9 = 900_000; levy = 100_000
	levy := math.NewInt(100_000)
	levyCoins := sdk.NewCoins(sdk.NewCoin(bondDenom, levy))

	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, stored))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), addr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, spendable)))
	s.bankKeeper.EXPECT().
		SendCoinsFromAccountToModule(gomock.Any(), addr, types.ModuleName, levyCoins).
		Return(nil)
	// Default sink is redistribution → SendCoinsFromModuleToModule to fee_collector.
	s.bankKeeper.EXPECT().
		SendCoinsFromModuleToModule(
			gomock.Any(),
			types.ModuleName,
			authtypes.FeeCollectorName,
			levyCoins,
		).Return(nil)

	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)

	// Verify reference accumulator was updated.
	accountState, err := s.demurrageKeeper.GetAccountState(s.ctx, addr)
	s.Require().NoError(err)
	s.True(accountState.ReferenceAccumulator.Equal(math.LegacyNewDecWithPrec(90, 2)),
		"reference accumulator must be updated to global after materialization")
}

func (s *KeeperTestSuite) TestMaterializeBalance_AlreadyCurrent() {
	addr := makeAddr(0x07)
	// ReferenceAccumulator already equals global → no bank calls, no-op.
	// (global is 1.0, new account ref is 1.0)
	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)
}

// TestMaterializeBalance_DustSweep verifies that when the effective spendable
// portion decays below one base unit (TruncateInt → 0), the entire spendable
// balance is levied rather than leaving an uncollectable sub-unit residual.
func (s *KeeperTestSuite) TestMaterializeBalance_DustSweep() {
	addr := makeAddr(0x24)
	// RefAcc = 1.0; GlobalAcc = 0.01 → ratio = 0.01.
	// With stored = 5 uatone: effectiveSpendable = floor(5 * 0.01) = 0.
	// Dust sweep: levy the entire 5 uatone.
	s.setRefAccumulator(addr, math.LegacyOneDec())
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(1, 2)) // 0.01

	stored := math.NewInt(5)
	levy := math.NewInt(5) // entire balance swept
	levyCoins := sdk.NewCoins(sdk.NewCoin(bondDenom, levy))

	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, stored))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), addr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, stored))) // fully spendable
	s.bankKeeper.EXPECT().
		SendCoinsFromAccountToModule(gomock.Any(), addr, types.ModuleName, levyCoins).
		Return(nil)
	s.bankKeeper.EXPECT().
		SendCoinsFromModuleToModule(
			gomock.Any(),
			types.ModuleName,
			authtypes.FeeCollectorName,
			levyCoins,
		).Return(nil)

	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)

	// Entire balance levied → account state must be GC'd.
	// GetAccountState for a non-existent account defaults to current GlobalAcc.
	accountState, err := s.demurrageKeeper.GetAccountState(s.ctx, addr)
	s.Require().NoError(err)
	s.True(accountState.ReferenceAccumulator.Equal(math.LegacyNewDecWithPrec(1, 2)),
		"GC'd account should default RefAcc to current global, got %s",
		accountState.ReferenceAccumulator)
}

