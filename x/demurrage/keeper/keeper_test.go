package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	demurrage "github.com/cosmos/cosmos-sdk/x/demurrage"
	"github.com/cosmos/cosmos-sdk/x/demurrage/keeper"
	demurragetestutil "github.com/cosmos/cosmos-sdk/x/demurrage/testutil"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
)

// KeeperTestSuite is the shared test suite for all demurrage keeper tests.
type KeeperTestSuite struct {
	suite.Suite

	ctx                sdk.Context
	demurrageKeeper    keeper.Keeper
	accountKeeper      *demurragetestutil.MockAccountKeeper
	bankKeeper         *demurragetestutil.MockBankKeeper
	stakingKeeper      *demurragetestutil.MockStakingKeeper
	distributionKeeper *demurragetestutil.MockDistributionKeeper
	msgServer          types.MsgServer
}

func TestKeeperTestSuite(t *testing.T) {
	suite.Run(t, new(KeeperTestSuite))
}

func (s *KeeperTestSuite) SetupTest() {
	encCfg := moduletestutil.MakeTestEncodingConfig(demurrage.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(
		s.T(),
		key,
		storetypes.NewTransientStoreKey("transient_test"),
	)
	s.ctx = testCtx.Ctx

	ctrl := gomock.NewController(s.T())
	s.accountKeeper = demurragetestutil.NewMockAccountKeeper(ctrl)
	s.bankKeeper = demurragetestutil.NewMockBankKeeper(ctrl)
	s.stakingKeeper = demurragetestutil.NewMockStakingKeeper(ctrl)
	s.distributionKeeper = demurragetestutil.NewMockDistributionKeeper(ctrl)
	// NewKeeper panics if the module account address is nil; also used by IsExempt
	// and sinkCommunityPool, so set up with AnyTimes for the module account address.
	s.accountKeeper.EXPECT().
		GetModuleAddress(types.ModuleName).
		Return(sdk.AccAddress([]byte("demurrage_module_acct"))).
		AnyTimes()

	// IsExemptAddress iterates through all exempt module account names on every
	// materialization call.  Provide a catch-all so tests don't need to set up
	// each one individually.  Use a distinct, non-colliding address per name so
	// exempt-list tests remain meaningful.
	for i, name := range types.DefaultParams().ExemptModuleAccounts {
		addr := make([]byte, 20)
		addr[0] = byte(i + 1) // 0x01 … 0x05, distinct from test accounts
		s.accountKeeper.EXPECT().
			GetModuleAddress(name).
			Return(sdk.AccAddress(addr)).
			AnyTimes()
	}

	s.demurrageKeeper = keeper.NewKeeper(
		encCfg.Codec,
		storeService,
		s.accountKeeper,
		s.bankKeeper,
		s.stakingKeeper,
		s.distributionKeeper,
		authtypes.NewModuleAddress(govtypes.ModuleName).String(),
	)

	s.msgServer = keeper.NewMsgServerImpl(s.demurrageKeeper)

	// Seed default params and state.
	s.Require().NoError(s.demurrageKeeper.Params.Set(s.ctx, types.DefaultParams()))
	s.Require().NoError(s.demurrageKeeper.State.Set(s.ctx, types.DemurrageState{
		CurrentAnnualRate: types.DefaultParams().DemurrageRateMin,
		GlobalAccumulator: math.LegacyOneDec(),
	}))
}
