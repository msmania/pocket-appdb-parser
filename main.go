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

func main() {
	var appDbDir string
	if len(os.Args) >= 2 {
		appDbDir = os.Args[1]
	} else {
		appDbDir = os.ExpandEnv("$HOME/.pocket/data/application.db")
	}

	var height int64
	if len(os.Args) >= 3 {
		height, _ = strconv.ParseInt(os.Args[2], 10, 64)
	}

	srcDb, err := leveldb.OpenFile(appDbDir, nil)
	if err != nil {
		log.Fatal("Failed to open db: " + appDbDir)
	}
	defer srcDb.Close()

	if height == 0 {
		height = getLatestHeight(srcDb)
	}
	dumpCommitInfo(srcDb, height)
}
