package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/tendermint/tendermint/rpc/client"
	"github.com/terra-project/core/app"

	sdk "github.com/cosmos/cosmos-sdk/types"
	utils "github.com/cosmos/cosmos-sdk/x/auth/client/utils"
	terra_util "github.com/terra-project/core/types/util"

	auth_types "github.com/cosmos/cosmos-sdk/x/auth/types"
	terra_types "github.com/terra-project/core/x/oracle"
)

var (
	TERRA_NODE_URI     = "http://localhost:26657"
	TERRA_REST         = "http://localhost:1317"
	TERRA_KEYBASE_DIR  = "/Users/mumu/.terracli"
	TERRA_KEYNAME      = "q"
	TERRA_KEY_PASSWORD = "12345678"
	TERRA_CHAIN_ID     = "terra-q"
	BAND_REST          = "http://guanyu-devnet.bandchain.org/rest/oracle/request_search?oid=64&calldata=000000044c554e410000000000002710&min_count=4&ask_count=4"
	VALIDATOR_ADDRESS  = "terravaloper1hwjr0j6v5s8cuwtvza9jaqz7s3nfnxyw4r6st6"
	cdc                = app.MakeCodec()
)

type BandResponse struct {
	Height int64      `json:"height,string"`
	Result BandResult `json:"result"`
}

type RawRequests struct {
	ExternalID   uint64 `json:"external_id,string"`
	DataSourceID uint64 `json:"data_source_id,string"`
	Calldata     []byte `json:"calldata,string"`
}

type Request struct {
	OracleScriptID      uint64        `json:"oracle_script_id,string"`
	Calldata            []byte        `json:"calldata,string"`
	RequestedValidators []string      `json:"requested_validators"`
	MinCount            uint64        `json:"min_count,string"`
	RequestHeight       uint64        `json:"request_height,string"`
	RequestTime         time.Time     `json:"request_time"`
	ClientID            string        `json:"client_id"`
	RawRequests         []RawRequests `json:"raw_requests"`
}

type RawReports struct {
	ExternalID uint64 `json:"external_id,string"`
	Data       string `json:"data"`
}

type Reports struct {
	Validator       string       `json:"validator"`
	InBeforeResolve bool         `json:"in_before_resolve"`
	RawReports      []RawReports `json:"raw_reports"`
}

type RequestPacketData struct {
	ClientID       string `json:"client_id"`
	OracleScriptID uint64 `json:"oracle_script_id,string"`
	Calldata       []byte `json:"calldata,string"`
	AskCount       uint64 `json:"ask_count,string"`
	MinCount       uint64 `json:"min_count,string"`
}

type ResponsePacketData struct {
	ClientID      string `json:"client_id"`
	RequestID     uint64 `json:"request_id,string"`
	AnsCount      uint64 `json:"ans_count,string"`
	RequestTime   uint64 `json:"request_time,string"`
	ResolveTime   uint64 `json:"resolve_time,string"`
	ResolveStatus uint8  `json:"resolve_status,string"`
	Result        []byte `json:"result,string"`
}

type Packet struct {
	RequestPacketData  RequestPacketData  `json:"RequestPacketData"`
	ResponsePacketData ResponsePacketData `json:"ResponsePacketData"`
}

type BandResult struct {
	Request Request   `json:"request"`
	Reports []Reports `json:"reports"`
	Result  Packet    `json:"result"`
}

// GenerateRandomBytes returns securely generated random bytes.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return nil, err
	}

	return b, nil
}

// GenerateRandomString returns a securely generated random string.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func generateRandomString(n int) (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	bytes, err := generateRandomBytes(n)
	if err != nil {
		return "", err
	}
	for i, b := range bytes {
		bytes[i] = letters[b%byte(len(letters))]
	}
	return string(bytes), nil
}

type Feeder struct {
	terraClient       *client.HTTP
	Params            terra_types.Params
	validator         sdk.ValAddress
	LastPrevoteRound  int64
	LatestBlockHeight int64
}

func (f *Feeder) fetchParams() {
	res, err := f.terraClient.ABCIQuery(fmt.Sprintf("custom/%s/%s", terra_types.QuerierRoute, terra_types.QueryParameters), nil)
	err = cdc.UnmarshalJSON(res.Response.GetValue(), &f.Params)
	if err != nil {
		fmt.Println("Fail to unmarshal Params json", err.Error())
		return
	}
}

func (f *Feeder) getPrevote() (terra_types.ExchangeRatePrevotes, error) {
	erps := terra_types.ExchangeRatePrevotes{}
	params := terra_types.NewQueryPrevotesParams(f.validator, "")

	bz, err := cdc.MarshalJSON(params)
	if err != nil {
		fmt.Println("Fail to marshal prevote params", err.Error())
		return erps, err
	}

	res, err := f.terraClient.ABCIQuery(fmt.Sprintf("custom/%s/%s", terra_types.QuerierRoute, terra_types.QueryPrevotes), bz)

	err = cdc.UnmarshalJSON(res.Response.GetValue(), &erps)
	if err != nil {
		fmt.Println("Fail to unmarshal Params json", err.Error())
		return erps, err
	}

	return erps, nil
}

func (f *Feeder) broadcast(msgs []sdk.Msg) (*sdk.TxResponse, error) {
	keybase, err := keys.NewKeyBaseFromDir(TERRA_KEYBASE_DIR)
	if err != nil {
		fmt.Println("Fail to create keybase from dir :", err.Error())
		return nil, err
	}

	txBldr := auth_types.NewTxBuilderFromCLI().
		WithTxEncoder(auth_types.DefaultTxEncoder(cdc)).
		WithKeybase(keybase)

	cliCtx := context.NewCLIContext().
		WithCodec(cdc).
		WithClient(f.terraClient).
		WithNodeURI(TERRA_NODE_URI).
		WithTrustNode(true).
		WithFromAddress(sdk.AccAddress(f.validator)).
		WithBroadcastMode("block")

	ptxBldr, err := utils.PrepareTxBuilder(txBldr, cliCtx)
	if err != nil {
		fmt.Println("Fail to prepare tx builder :", err.Error())
		return nil, err
	}

	txBytes, err := ptxBldr.WithChainID(TERRA_CHAIN_ID).BuildAndSign(
		TERRA_KEYNAME,
		TERRA_KEY_PASSWORD,
		msgs,
	)
	if err != nil {
		fmt.Println("Fail to build and sign the transaction :", err.Error())
		return nil, err
	}

	res, err := cliCtx.BroadcastTx(txBytes)
	if err != nil {
		fmt.Println("Fail to broadcast to a Tendermint node :", err.Error())
		return nil, err
	}

	return &res, nil
}

func NewFeeder() Feeder {
	feeder := Feeder{}
	feeder.terraClient = client.NewHTTP(TERRA_NODE_URI, "/websocket")
	valAddress, err := sdk.ValAddressFromBech32(VALIDATOR_ADDRESS)
	if err != nil {
		fmt.Println("Fail to parse validator address", err.Error())
		panic(err)
	}
	feeder.validator = valAddress
	return feeder
}

func InitSDKConfig() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(terra_util.Bech32PrefixAccAddr, terra_util.Bech32PrefixAccPub)
	config.SetBech32PrefixForValidator(terra_util.Bech32PrefixValAddr, terra_util.Bech32PrefixValPub)
	config.SetBech32PrefixForConsensusNode(terra_util.Bech32PrefixConsAddr, terra_util.Bech32PrefixConsPub)
	config.Seal()
}

func getPriceFromBAND() map[string]sdk.Dec {
	resp, err := http.Get(BAND_REST)
	if err != nil {
		fmt.Println(err.Error())
		return nil
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err.Error())
		return nil
	}

	br := BandResponse{}
	json.Unmarshal(body, &br)

	fmt.Println(br)
	return nil
}

func main() {

	fmt.Println("Start ...")

	InitSDKConfig()

	feeder := NewFeeder()

	for feeder.Params.VotePeriod == 0 {
		feeder.fetchParams()
		time.Sleep(1 * time.Second)
	}

	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("Unknown error", r)
				}

				time.Sleep(1 * time.Second)
			}()

			status, err := feeder.terraClient.Status()
			if err != nil {
				fmt.Println("Fail to fetch status", err.Error())
				return
			}

			feeder.LatestBlockHeight = status.SyncInfo.LatestBlockHeight
			nextRound := feeder.LatestBlockHeight / feeder.Params.VotePeriod

			fmt.Println(feeder.LatestBlockHeight, nextRound)

			if nextRound > feeder.LastPrevoteRound {
				_, err := feeder.getPrevote()
				if err != nil {
					fmt.Println(err.Error())
					return
				}

				salt, err := generateRandomString(10)
				if err != nil {
					fmt.Println(err.Error())
					return
				}

				voteHash, err := terra_types.VoteHash(salt, sdk.NewDec(99), "uusd", feeder.validator)
				if err != nil {
					fmt.Println(err.Error())
					return
				}

				msg := []sdk.Msg{terra_types.NewMsgExchangeRatePrevote(fmt.Sprintf("%x", voteHash), "uusd", sdk.AccAddress(feeder.validator), feeder.validator)}

				feeder.broadcast(msg)
			}
		}()
	}

	fmt.Println(feeder)

}
