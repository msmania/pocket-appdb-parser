package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/pokt-network/pocket-core/app"
	"github.com/pokt-network/pocket-core/codec"
	"github.com/pokt-network/pocket-core/codec/types"
	"github.com/pokt-network/pocket-core/crypto"
	"github.com/pokt-network/pocket-core/store/rootmulti"
	nodeTypes "github.com/pokt-network/pocket-core/x/nodes/types"
	pcTypes "github.com/pokt-network/pocket-core/x/pocketcore/types"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
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

func getChildrenFromNode(buf []byte) (
	version int64,
	leftHash, rightHash []byte,
	key, value []byte,
	err error) {
	height, n, cause := amino.DecodeInt8(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.height")
		return
	}

	buf = buf[n:]

	_, n, cause = amino.DecodeVarint(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.size")
		return
	}
	buf = buf[n:]

	version, n, cause = amino.DecodeVarint(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.version")
		return
	}
	buf = buf[n:]

	key, n, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.key")
		return
	}
	buf = buf[n:]

	if height == 0 {
		// this node is a leaf
		value, _, cause = amino.DecodeByteSlice(buf)
		if cause != nil {
			err = errors.Wrap(cause, "decoding node.value")
			return
		}
		return
	}

	leftHash, n, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.leftHash")
		return
	}
	buf = buf[n:]

	rightHash, _, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.rightHash")
		return
	}

	return
}

var (
	AllValidatorsKey = []byte{0x21}
)

func parseNode(key, value []byte) (res string) {
	if bytes.HasPrefix(key, AllValidatorsKey) {
		var valNew nodeTypes.Validator
		err := app.Codec().UnmarshalBinaryLengthPrefixed(value, &valNew, 0)
		if err == nil {
			return "Val " + valNew.Address.String()
		}

		var valLegacy nodeTypes.LegacyValidator
		err = app.Codec().UnmarshalBinaryLengthPrefixed(value, &valLegacy, 0)
		if err == nil {
			return "LVal " + valNew.Address.String()
		}

		return "err:" + err.Error()
	}

	return
}

func dfs(db *leveldb.DB, prefix, hash []byte, depth int) {
	if len(hash) == 0 {
		return
	}

	nodeKey := make([]byte, len(prefix), len(prefix)+1+len(hash))
	copy(nodeKey, prefix)
	nodeKey = append(nodeKey, 'n')
	nodeKey = append(nodeKey, hash...)

	nodeValue, err := db.Get(nodeKey, nil)
	if err != nil {
		log.Fatalf("Not found: %x", nodeKey)
		return
	}

	ver, l, r, k, v, err := getChildrenFromNode(nodeValue)
	if err != nil {
		log.Fatalf("%s: %s", err.Error(), nodeKey)
		return
	}

	var nodeType rune
	if len(l) == 0 {
		if len(r) == 0 {
			nodeType = 'l'
		} else {
			nodeType = '!'
		}
	} else if len(r) == 0 {
		nodeType = '!'
	} else {
		nodeType = 'n'
	}

	kv := ""
	if nodeType == 'l' {
		if bytes.Contains(prefix, []byte("k:params")) {
			kv = fmt.Sprintf("%v -> %v", string(k), string(v))
		} else if bytes.Contains(prefix, []byte("k:pos")) {
			kv = parseNode(k, v)
		}
	}

	fmt.Printf("%s%x %c %v %v\n",
		strings.Repeat(" ", depth),
		hash,
		nodeType,
		ver,
		kv)

	dfs(db, prefix, l, depth+1)
	dfs(db, prefix, r, depth+1)
}

func getBlockRange(blockRange string, latest int64) []int64 {
	safeParse := func(str string, def int64) int64 {
		if len(str) == 0 {
			return def
		}
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return n
		}
		return int64(0)
	}

	tokens := strings.Split(blockRange, ":")
	if len(tokens) == 2 {
		a := safeParse(tokens[0], latest)
		if a <= 0 {
			log.Fatalln("invalid token:", tokens[0])
		}
		b := safeParse(tokens[1], latest)
		if b <= 0 {
			log.Fatalln("invalid token:", tokens[1])
		}

		seq := make([]int64, b-a+1)
		for i := 0; i < len(seq); i++ {
			seq[i] = a + int64(i)
		}
		return seq
	}

	tokens = strings.Split(blockRange, ",")
	if len(tokens) == 1 {
		a, err := strconv.ParseInt(tokens[0], 10, 64)
		if err != nil || a == 0 {
			log.Fatalln("invalid token:", tokens[0])
		}
		if a < 0 {
			seq := make([]int64, -a)
			for i := int64(0); i < -a; i++ {
				seq[i] = latest - i
			}
			return seq
		}
	}

	seq := make([]int64, len(tokens))
	for i := 0; i < len(seq); i++ {
		a, err := strconv.ParseInt(tokens[i], 10, 64)
		if err != nil {
			log.Fatalln("invalid token:", tokens[i])
		}
		seq[i] = a
	}
	return seq
}

func dumpOrphan(db *leveldb.DB, store string) {
	prefix := "s/k:" + store + "/o"
	it := db.NewIterator(util.BytesPrefix([]byte(prefix)), nil)
	for it.Next() {
		orphanKey := it.Key()
		orphanValue := it.Value()

		prefixLen := len(prefix)
		if len(orphanKey) != prefixLen+8+8+len(orphanValue) {
			fmt.Println("Unexpected length at: ", hex.EncodeToString(orphanKey))
			continue
		}
		if !bytes.HasSuffix(orphanKey, orphanValue) {
			fmt.Println("Unexpected value at: ", hex.EncodeToString(orphanKey))
			continue
		}

		verTo := binary.BigEndian.Uint64(orphanKey[prefixLen : prefixLen+8])
		prefixLen = prefixLen + 8
		verFrom := binary.BigEndian.Uint64(orphanKey[prefixLen : prefixLen+8])
		fmt.Printf("%s [%v-%v] %s\n",
			prefix, verFrom, verTo, hex.EncodeToString(orphanValue))
	}
}

func dumpCommitInfo(db *leveldb.DB, heights []int64) {
	cdc := codec.NewCodec(types.NewInterfaceRegistry())
	pcTypes.RegisterCodec(cdc)
	crypto.RegisterAmino(cdc.AminoCodec().Amino)

	for _, height := range heights {
		ciBytes, err := db.Get([]byte(fmt.Sprintf("s/%d", height)), nil)
		if err != nil {
			fmt.Println("Unexpected commitInfo at ", height)
			continue
		}

		var cInfo rootmulti.CommitInfo
		err = cdc.LegacyUnmarshalBinaryLengthPrefixed(ciBytes, &cInfo)
		if err != nil {
			fmt.Println("Failed to unmarhsal commitInfo at ", height)
			continue
		}

		fmt.Println("Version:", cInfo.Version)
		for _, store := range cInfo.StoreInfos {
			fmt.Printf(
				"%12s:%v %v\n",
				store.Name,
				store.Core.CommitID.Version,
				hex.EncodeToString(store.Core.CommitID.Hash),
			)
		}
	}
}

func main() {
	if len(os.Args) < 4 {
		fmt.Print(`USAGE:
dump <appdb> <store> <range>
dump <appdb> ci <range>
dump <appdb> !<store> <hash>

range:
  100
	1,100
	1:100
	:1
`)
		return
	}

	dir := os.Args[1]
	store := os.Args[2]
	blockrange := os.Args[3]

	srcDb, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		log.Fatal("Failed to open db: " + dir)
	}

	if strings.HasPrefix(store, "!") {
		// hash, err := hex.DecodeString(blockrange)
		// if err != nil {
		// 	log.Fatal("Invalid hash: " + blockrange)
		// }
		dumpOrphan(srcDb, store[1:])
		return
	}

	latestHeight := getLatestHeight(srcDb)
	heights := getBlockRange(blockrange, latestHeight)

	if store == "ci" {
		dumpCommitInfo(srcDb, heights)
		return
	}

	for _, height := range heights {
		prefix := []byte("s/k:" + store + "/")
		fmt.Printf("# %s%v\n", string(prefix), height)

		key := make([]byte, 0, len(prefix)+1+8)
		key = append(key, prefix...)
		key = append(key, 'r')
		key = binary.BigEndian.AppendUint64(key, uint64(height))

		hash, err := srcDb.Get(key, nil)
		if err != nil {
			log.Fatal("Failed to get data")
		}

		dfs(srcDb, prefix, hash, 0)
	}
}
