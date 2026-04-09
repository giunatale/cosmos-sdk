package keeper_test

import (
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

func (s *KeeperTestSuite) TestMsgUpdateParams_AuthorityAccepted() {
	authority := s.demurrageKeeper.GetAuthority()
	newParams := types.DefaultParams()
	newParams.SinkMode = types.SinkModeBurn

	_, err := s.msgServer.UpdateParams(s.ctx, &types.MsgUpdateParams{
		Authority: authority,
		Params:    newParams,
	})
	s.Require().NoError(err)

	stored, err := s.demurrageKeeper.Params.Get(s.ctx)
	s.Require().NoError(err)
	s.Equal(types.SinkModeBurn, stored.SinkMode)
}

func (s *KeeperTestSuite) TestMsgUpdateParams_UnauthorizedRejected() {
	_, err := s.msgServer.UpdateParams(s.ctx, &types.MsgUpdateParams{
		Authority: authtypes.NewModuleAddress("not_gov").String(),
		Params:    types.DefaultParams(),
	})
	s.Require().Error(err)
}

func (s *KeeperTestSuite) TestMsgUpdateParams_InvalidParamsRejected() {
	authority := s.demurrageKeeper.GetAuthority()
	bad := types.DefaultParams()
	bad.DemurrageRateMin = bad.DemurrageRateMax.Add(bad.DemurrageRateMax) // min > max

	_, err := s.msgServer.UpdateParams(s.ctx, &types.MsgUpdateParams{
		Authority: authority,
		Params:    bad,
	})
	s.Require().Error(err)
}
