package keeper_test

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
	"go.uber.org/mock/gomock"
)

// setParams is a helper to override only the sink mode for a test.
func (s *KeeperTestSuite) setSinkMode(mode string) {
	params, err := s.demurrageKeeper.Params.Get(s.ctx)
	s.Require().NoError(err)
	params.SinkMode = mode
	s.Require().NoError(s.demurrageKeeper.Params.Set(s.ctx, params))
}

// materializeWithLevy is a helper that sets up an account with a 10% decay so that
// MaterializeBalance produces a non-zero levy and routes it to the configured sink.
// Returns the levy amount (100_000 uatone for 1M balance at 0.9 global accumulator).
func (s *KeeperTestSuite) materializeWithLevy(addr sdk.AccAddress) math.Int {
	// Account was created at genesis (refAcc = 1.0); global has since decayed to 0.9.
	s.setRefAccumulator(addr, math.LegacyOneDec())
	s.setGlobalAccumulator(math.LegacyNewDecWithPrec(90, 2)) // 0.9

	stored := math.NewInt(1_000_000)
	levy := math.NewInt(100_000) // 1_000_000 - 900_000

	s.stakingKeeper.EXPECT().BondDenom(gomock.Any()).Return(bondDenom, nil)
	s.bankKeeper.EXPECT().
		GetBalance(gomock.Any(), addr, bondDenom).
		Return(sdk.NewCoin(bondDenom, stored))
	s.bankKeeper.EXPECT().
		SpendableCoins(gomock.Any(), addr).
		Return(sdk.NewCoins(sdk.NewCoin(bondDenom, stored)))
	s.bankKeeper.EXPECT().
		SendCoinsFromAccountToModule(
			gomock.Any(), addr, types.ModuleName,
			sdk.NewCoins(sdk.NewCoin(bondDenom, levy)),
		).Return(nil)
	return levy
}

func (s *KeeperTestSuite) TestSink_Burn() {
	s.setSinkMode(types.SinkModeBurn)
	addr := makeAddr(0x10)
	levy := s.materializeWithLevy(addr)

	s.bankKeeper.EXPECT().
		BurnCoins(gomock.Any(), types.ModuleName, sdk.NewCoins(sdk.NewCoin(bondDenom, levy))).
		Return(nil)

	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)
}

func (s *KeeperTestSuite) TestSink_CommunityPool() {
	s.setSinkMode(types.SinkModeCommunityPool)
	addr := makeAddr(0x11)
	levy := s.materializeWithLevy(addr)

	moduleAddr := sdk.AccAddress([]byte("demurrage_module_acct"))
	s.distributionKeeper.EXPECT().
		FundCommunityPool(
			gomock.Any(),
			sdk.NewCoins(sdk.NewCoin(bondDenom, levy)),
			moduleAddr,
		).Return(nil)

	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)
}

func (s *KeeperTestSuite) TestSink_Redistribution() {
	// Default sink mode is already redistribution; this test is explicit for clarity.
	s.setSinkMode(types.SinkModeRedistribution)
	addr := makeAddr(0x12)
	levy := s.materializeWithLevy(addr)

	s.bankKeeper.EXPECT().
		SendCoinsFromModuleToModule(
			gomock.Any(),
			types.ModuleName,
			authtypes.FeeCollectorName,
			sdk.NewCoins(sdk.NewCoin(bondDenom, levy)),
		).Return(nil)

	err := s.demurrageKeeper.MaterializeBalance(s.ctx, addr)
	s.Require().NoError(err)
}
