package keeper_test

import (
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

func (s *KeeperTestSuite) TestGenesis_RoundTrip() {
	// Write non-default state.
	customParams := types.DefaultParams()
	customParams.SinkMode = types.SinkModeBurn

	customState := types.DemurrageState{
		CurrentAnnualRate: math.LegacyNewDecWithPrec(15, 2),
		GlobalAccumulator: math.LegacyNewDecWithPrec(92, 2),
	}

	s.demurrageKeeper.InitGenesis(s.ctx, &types.GenesisState{
		Params: customParams,
		State:  customState,
	})

	exported := s.demurrageKeeper.ExportGenesis(s.ctx)
	s.Require().NotNil(exported)

	s.True(exported.Params.GoalBonded.Equal(customParams.GoalBonded))
	s.Equal(types.SinkModeBurn, exported.Params.SinkMode)
	s.True(exported.State.CurrentAnnualRate.Equal(customState.CurrentAnnualRate))
	s.True(exported.State.GlobalAccumulator.Equal(customState.GlobalAccumulator))
}

func (s *KeeperTestSuite) TestGenesis_DefaultState() {
	gs := types.DefaultGenesisState()
	s.demurrageKeeper.InitGenesis(s.ctx, gs)
	exported := s.demurrageKeeper.ExportGenesis(s.ctx)

	s.True(exported.State.GlobalAccumulator.Equal(math.LegacyOneDec()),
		"default genesis global accumulator must be 1.0")
	s.True(exported.State.CurrentAnnualRate.Equal(gs.Params.DemurrageRateMin),
		"default genesis rate must equal DemurrageRateMin")
}

func (s *KeeperTestSuite) TestValidateGenesis_Valid() {
	gs := types.DefaultGenesisState()
	s.Require().NoError(types.ValidateGenesis(*gs))
}

func (s *KeeperTestSuite) TestValidateGenesis_AccumulatorTooHigh() {
	gs := types.DefaultGenesisState()
	gs.State.GlobalAccumulator = math.LegacyNewDecWithPrec(110, 2) // > 1.0
	s.Require().Error(types.ValidateGenesis(*gs))
}

func (s *KeeperTestSuite) TestValidateGenesis_NegativeRate() {
	gs := types.DefaultGenesisState()
	gs.State.CurrentAnnualRate = math.LegacyNewDecWithPrec(-1, 2)
	s.Require().Error(types.ValidateGenesis(*gs))
}
