package eth_test

import (
	"encoding/json"
	"testing"

	"math/big"

	"chainlink/core/eth"
	"chainlink/core/internal/cltest"
	"chainlink/core/internal/mocks"
	strpkg "chainlink/core/store"
	"chainlink/core/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCallerSubscriberClient_GetTxReceipt(t *testing.T) {
	response := cltest.MustReadFile(t, "testdata/getTransactionReceipt.json")
	mockServer, wsCleanup := cltest.NewWSServer(string(response))
	defer wsCleanup()
	config := cltest.NewConfigWithWSServer(t, mockServer)
	store, cleanup := cltest.NewStoreWithConfig(config)
	defer cleanup()

	ec := store.TxManager.(*strpkg.EthTxManager).Client

	hash := common.HexToHash("0xb903239f8543d04b5dc1ba6579132b143087c68db1b2168786408fcbce568238")
	receipt, err := ec.GetTxReceipt(hash)
	assert.NoError(t, err)
	assert.Equal(t, hash, receipt.Hash)
	assert.Equal(t, cltest.Int(uint64(11)), receipt.BlockNumber)
}

func TestTxReceipt_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantLogLen int
	}{
		{"basic", "testdata/getTransactionReceipt.json", 0},
		{"runlog request", "testdata/runlogReceipt.json", 4},
		{"runlog response", "testdata/responseReceipt.json", 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jsonStr := cltest.JSONFromFixture(t, test.path).Get("result").String()
			var receipt eth.TxReceipt
			err := json.Unmarshal([]byte(jsonStr), &receipt)
			require.NoError(t, err)

			assert.Equal(t, test.wantLogLen, len(receipt.Logs))
		})
	}
}

func TestTxReceipt_FulfilledRunlog(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"basic", "testdata/getTransactionReceipt.json", false},
		{"runlog request", "testdata/runlogReceipt.json", false},
		{"runlog response", "testdata/responseReceipt.json", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := cltest.TxReceiptFromFixture(t, test.path)
			assert.Equal(t, test.want, receipt.FulfilledRunLog())
		})
	}
}

func TestCallerSubscriberClient_GetNonce(t *testing.T) {
	t.Parallel()
	app, cleanup := cltest.NewApplicationWithKey(t, cltest.EthMockRegisterChainID)
	defer cleanup()
	app.EthMock.Register("eth_getTransactionCount", "0x0100")
	ethClientObject := app.Store.TxManager.(*strpkg.EthTxManager).Client
	require.NoError(t, app.Start())

	app.EthMock.Register("eth_getTransactionCount", "0x0100")
	result, err := ethClientObject.GetNonce(cltest.NewAddress())
	assert.NoError(t, err)
	var expected uint64 = 256
	assert.Equal(t, result, expected)
}

func TestCallerSubscriberClient_SendRawTx(t *testing.T) {
	t.Parallel()
	app, cleanup := cltest.NewApplicationWithKey(t, cltest.LenientEthMock)
	defer cleanup()

	ethMock := app.EthMock
	ethClientObject := app.Store.TxManager.(*strpkg.EthTxManager).Client
	ethMock.Register("eth_sendRawTransaction", common.Hash{1})

	require.NoError(t, app.Start())
	result, err := ethClientObject.SendRawTx("test")
	assert.NoError(t, err)
	assert.Equal(t, result, common.Hash{1})
}

func TestCallerSubscriberClient_GetEthBalance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"basic", "0x0100", "0.000000000000000256"},
		{"larger than signed 64 bit integer", "0x4b3b4ca85a86c47a098a224000000000", "100000000000000000000.000000000000000000"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, cleanup := cltest.NewApplicationWithKey(t)

			ethMock := app.EthMock
			ethMock.Context("app.Start()", func(meth *cltest.EthMock) {
				meth.Register("eth_getTransactionCount", "0x1")
				meth.Register("eth_chainId", app.Store.Config.ChainID())
			})
			defer cleanup()
			require.NoError(t, app.Start())
			ethClientObject := app.Store.TxManager.(*strpkg.EthTxManager).Client

			ethMock.Register("eth_getBalance", test.input)
			result, err := ethClientObject.GetEthBalance(cltest.NewAddress())
			assert.NoError(t, err)
			assert.Equal(t, test.expected, result.String())
		})
	}
}

func TestCallerSubscriberClient_GetERC20Balance(t *testing.T) {
	t.Parallel()
	app, cleanup := cltest.NewApplicationWithKey(t, cltest.LenientEthMock)
	defer cleanup()
	ethMock := app.EthMock
	ethClientObject := app.Store.TxManager.(*strpkg.EthTxManager).Client

	ethMock.Register("eth_call", "0x0100") // 256

	require.NoError(t, app.Start())

	result, err := ethClientObject.GetERC20Balance(cltest.NewAddress(), cltest.NewAddress())
	assert.NoError(t, err)
	expected := big.NewInt(256)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)

	ethMock.Register("eth_call", "0x4b3b4ca85a86c47a098a224000000000") // 1e38
	result, err = ethClientObject.GetERC20Balance(cltest.NewAddress(), cltest.NewAddress())
	expected = big.NewInt(0)
	expected.SetString("100000000000000000000000000000000000000", 10)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestCallerSubscriberClient_GetAggregatorPrice(t *testing.T) {
	caller := new(mocks.CallerSubscriber)
	ethClient := &eth.CallerSubscriberClient{CallerSubscriber: caller}
	address := cltest.NewAddress()

	// aggregatorLatestAnswerID is the first 4 bytes of the keccak256 of
	// Chainlink's aggregator latestAnswer function.
	const aggregatorLatestAnswerID = "50d25bcd"
	aggregatorLatestAnswerSelector := eth.HexToFunctionSelector(aggregatorLatestAnswerID)

	expectedCallArgs := eth.CallArgs{
		To:   address,
		Data: aggregatorLatestAnswerSelector.Bytes(),
	}

	tests := []struct {
		name, response string
		precision      int32
		expectation    decimal.Decimal
	}{
		{"hex - Zero", "0x", 2, decimal.NewFromFloat(0)},
		{"hex", "0x0100", 2, decimal.NewFromFloat(2.56)},
		{"decimal", "10000000000000", 11, decimal.NewFromInt(100)},
		{"large decimal", "52050000000000000000", 11, decimal.RequireFromString("520500000")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller.On("Call", mock.Anything, "eth_call", expectedCallArgs, "latest").Return(nil).
				Run(func(args mock.Arguments) {
					res := args.Get(0).(*string)
					*res = test.response
				})
			result, err := ethClient.GetAggregatorPrice(address, test.precision)
			require.NoError(t, err)
			assert.True(t, test.expectation.Equal(result))
			caller.AssertExpectations(t)
		})
	}
}

func TestCallerSubscriberClient_GetAggregatorRound(t *testing.T) {
	caller := new(mocks.CallerSubscriber)
	ethClient := &eth.CallerSubscriberClient{CallerSubscriber: caller}
	address := cltest.NewAddress()

	const aggregatorLatestRoundID = "668a0f02"
	aggregatorLatestRoundSelector := eth.HexToFunctionSelector(aggregatorLatestRoundID)

	expectedCallArgs := eth.CallArgs{
		To:   address,
		Data: aggregatorLatestRoundSelector.Bytes(),
	}
	large, ok := new(big.Int).SetString("52050000000000000000", 10)
	require.True(t, ok)

	tests := []struct {
		name, response string
		expectation    *big.Int
	}{
		{"zero", "0", big.NewInt(0)},
		{"small", "12", big.NewInt(12)},
		{"large", "52050000000000000000", large},
		{"hex zero default", "0x", big.NewInt(0)},
		{"hex zero", "0x0", big.NewInt(0)},
		{"hex", "0x0100", big.NewInt(256)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller.On("Call", mock.Anything, "eth_call", expectedCallArgs, "latest").Return(nil).
				Run(func(args mock.Arguments) {
					res := args.Get(0).(*string)
					*res = test.response
				})
			result, err := ethClient.GetAggregatorRound(address)
			require.NoError(t, err)
			assert.Equal(t, test.expectation, result)
			caller.AssertExpectations(t)
		})
	}
}

func TestCallerSubscriberClient_GetLatestSubmission(t *testing.T) {
	caller := new(mocks.CallerSubscriber)
	ethClient := &eth.CallerSubscriberClient{CallerSubscriber: caller}
	aggregatorAddress := cltest.NewAddress()
	oracleAddress := cltest.NewAddress()

	const aggregatorLatestSubmission = "bb07bacd"
	aggregatorLatestSubmissionSelector := eth.HexToFunctionSelector(aggregatorLatestSubmission)

	callData := utils.ConcatBytes(aggregatorLatestSubmissionSelector.Bytes(), oracleAddress.Hash().Bytes())

	expectedCallArgs := eth.CallArgs{
		To:   aggregatorAddress,
		Data: callData,
	}

	tests := []struct {
		name           string
		answer         int64
		round          int64
		expectedAnswer *big.Int
		expectedRound  *big.Int
	}{
		{"zero", 0, 0, big.NewInt(0), big.NewInt(0)},
		{"small", 8, 12, big.NewInt(8), big.NewInt(12)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller.On("Call", mock.Anything, "eth_call", expectedCallArgs, "latest").Return(nil).
				Run(func(args mock.Arguments) {
					res := args.Get(0).(*string)
					answerBytes, err := utils.EVMWordSignedBigInt(big.NewInt(test.answer))
					require.NoError(t, err)
					roundBytes, err := utils.EVMWordBigInt(big.NewInt(test.round))
					require.NoError(t, err)
					*res = hexutil.Encode(append(answerBytes, roundBytes...))
				})
			answer, round, err := ethClient.GetLatestSubmission(aggregatorAddress, oracleAddress)
			require.NoError(t, err)
			assert.Equal(t, test.expectedAnswer.String(), answer.String())
			assert.Equal(t, test.expectedRound.String(), round.String())
			caller.AssertExpectations(t)
		})
	}
}
