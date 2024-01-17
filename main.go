package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/pkg/errors"
	"github.com/pokt-network/pocket-core/codec"
	"github.com/pokt-network/pocket-core/codec/types"
	"github.com/pokt-network/pocket-core/crypto"
	"github.com/pokt-network/pocket-core/store/rootmulti"
	pcTypes "github.com/pokt-network/pocket-core/x/pocketcore/types"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/tendermint/go-amino"
	cryptoamino "github.com/tendermint/tendermint/crypto/encoding/amino"
	tmState "github.com/tendermint/tendermint/state"
)

func unmarshalBinaryLengthPrefixed(codec *amino.Codec, bz []byte, ptr interface{}) error {
	if len(bz) == 0 {
		return errors.New("cannot decode empty bytes")
	}

	// Read byte-length prefix.
	u64, n := binary.Uvarint(bz)
	if n < 0 {
		return errors.New("error reading msg byte-length prefix")
	}
	if u64 > uint64(len(bz)-n) {
		return errors.New("not enough bytes to read")
	} else if u64 < uint64(len(bz)-n) {
		return errors.New("bytes left over")
	}
	bz = bz[n:]
	return codec.UnmarshalBinaryBare(bz, ptr)
}

func getLatestHeight(db *leveldb.DB) int64 {
	keyLatest := []byte("s/latest")
	latestBytes, err := db.Get(keyLatest, nil)
	if err != nil {
		log.Fatal("Not found: ", string(keyLatest))
	}

	codec := amino.NewCodec()
	var latest int64
	err = unmarshalBinaryLengthPrefixed(codec, latestBytes, &latest)
	if err != nil {
		log.Fatal(err.Error())
	}

	return latest
}

func dumpCommitInfo(db *leveldb.DB, height int64) {
	cdc := codec.NewCodec(types.NewInterfaceRegistry())
	pcTypes.RegisterCodec(cdc)
	crypto.RegisterAmino(cdc.AminoCodec().Amino)

	ciBytes, err := db.Get([]byte(fmt.Sprintf("s/%d", height)), nil)
	if err != nil {
		fmt.Println("Unexpected commitInfo at ", height)
		return
	}

	var cInfo rootmulti.CommitInfo
	err = cdc.LegacyUnmarshalBinaryLengthPrefixed(ciBytes, &cInfo)
	if err != nil {
		fmt.Println("Failed to unmarhsal commitInfo at ", height)
		return
	}

	fmt.Printf("%d: %x\n", cInfo.Version, cInfo.Hash())
	for _, store := range cInfo.StoreInfos {
		fmt.Printf(
			"%12s:%v %v\n",
			store.Name,
			store.Core.CommitID.Version,
			hex.EncodeToString(store.Core.CommitID.Hash),
		)
	}
}

func dumpValidatorSet(db *leveldb.DB, height int64) {
	cdc := amino.NewCodec()
	cryptoamino.RegisterAmino(cdc)

	calcValidatorsKey := []byte(fmt.Sprintf("validatorsKey:%v", height))
	valsBytes, err := db.Get(calcValidatorsKey, nil)
	if err != nil {
		fmt.Println("Cannot read validatorsKey at ", height)
		return
	}

	var vals tmState.ValidatorsInfo
	err = cdc.UnmarshalBinaryBare(valsBytes, &vals)
	if err != nil {
		fmt.Println("Failed to unmarhsal validatorsKey at ", height)
		return
	}

	fmt.Printf("%d: Validators\n", height)
	for _, val := range vals.ValidatorSet.Validators {
		fmt.Printf("%v %v\n", val.Address, val.VotingPower)
	}
}

func main() {
	var dataDir string
	if len(os.Args) >= 2 {
		dataDir = os.Args[1]
	} else {
		dataDir = os.ExpandEnv("$HOME/.pocket/data")
	}

	appDbDir := dataDir + "/application.db"
	stateDbDir := dataDir + "/state.db"

	var height int64
	if len(os.Args) >= 3 {
		height, _ = strconv.ParseInt(os.Args[2], 10, 64)
	}

	srcAppDb, err := leveldb.OpenFile(appDbDir, nil)
	if err != nil {
		log.Fatal("Failed to open db: " + appDbDir)
	}
	defer srcAppDb.Close()

	srcStateDb, err := leveldb.OpenFile(stateDbDir, nil)
	if err != nil {
		log.Fatal("Failed to open db: " + stateDbDir)
	}
	defer srcStateDb.Close()

	if height == 0 {
		height = getLatestHeight(srcAppDb)
	}
	// dumpCommitInfo(srcAppDb, height)
	dumpValidatorSet(srcStateDb, height)
}
