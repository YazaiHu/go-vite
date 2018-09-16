package access

import (
	"encoding/binary"
	"errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/vitelabs/go-vite/chain_db/database"
	"github.com/vitelabs/go-vite/common/types"
	"github.com/vitelabs/go-vite/ledger"
)

type SnapshotChain struct {
	db *leveldb.DB
}

func NewSnapshotChain(db *leveldb.DB) *SnapshotChain {
	return &SnapshotChain{
		db: db,
	}
}

func (sc *SnapshotChain) getBlockHash(dbKey []byte) *types.Hash {
	hashBytes := dbKey[17:]
	hash, _ := types.BytesToHash(hashBytes)
	return &hash
}

func (sc *SnapshotChain) WriteSnapshotHash(batch *leveldb.Batch, hash *types.Hash, height uint64) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCKHASH, hash.Bytes())
	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, height)

	batch.Put(key, heightBytes)
}

func (sc *SnapshotChain) WriteSnapshotContent(batch *leveldb.Batch, snapshotHash *types.Hash, snapshotContent ledger.SnapshotContent) error {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTCONTENT, snapshotHash.Bytes())
	data, sErr := snapshotContent.DbSerialize()
	if sErr != nil {
		return sErr
	}
	batch.Put(key, data)
	return nil
}

func (sc *SnapshotChain) WriteSnapshotBlock(batch *leveldb.Batch, snapshotBlock *ledger.SnapshotBlock) error {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK, snapshotBlock.Height, snapshotBlock.Hash.Bytes())
	data, sErr := snapshotBlock.DbSerialize()
	if sErr != nil {
		return sErr
	}
	batch.Put(key, data)
	return nil
}

func (sc *SnapshotChain) GetLatestBlock() (*ledger.SnapshotBlock, error) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK)

	iter := sc.db.NewIterator(util.BytesPrefix(key), nil)
	defer iter.Release()

	if !iter.Last() {
		return nil, errors.New("GetLatestBlock failed. Because the SnapshotChain has no block")
	}

	sb := &ledger.SnapshotBlock{}
	sdErr := sb.DbDeserialize(iter.Value())

	if sdErr != nil {
		return nil, sdErr
	}

	sb.Hash = *sc.getBlockHash(iter.Key())

	return sb, nil
}

func (sc *SnapshotChain) GetGenesesBlock() (*ledger.SnapshotBlock, error) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK)

	iter := sc.db.NewIterator(util.BytesPrefix(key), nil)
	defer iter.Release()

	if !iter.Next() {
		return nil, errors.New("GetGenesesBlock failed. Because the SnapshotChain has no block")
	}

	sb := &ledger.SnapshotBlock{}
	sdErr := sb.DbDeserialize(iter.Value())

	if sdErr != nil {
		return nil, sdErr
	}

	sb.Hash = *sc.getBlockHash(iter.Key())

	return sb, nil
}

func (sc *SnapshotChain) GetSnapshotContent(snapshotHash *types.Hash) (ledger.SnapshotContent, error) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTCONTENT, snapshotHash.Bytes())
	data, err := sc.db.Get(key, nil)
	if err != nil {
		if err != leveldb.ErrNotFound {
			return nil, err
		}
		return nil, nil
	}

	snapshotContent := ledger.SnapshotContent{}
	snapshotContent.DbDeserialize(data)

	return snapshotContent, nil
}
func (sc *SnapshotChain) GetSnapshotBlocks(height uint64, count uint64, forward, containSnapshotContent bool) ([]*ledger.SnapshotBlock, error) {
	var blocks []*ledger.SnapshotBlock
	var startHeight, endHeight = uint64(0), uint64(0)
	if forward {
		startHeight = height
		endHeight = height + count
	} else {
		startHeight = height - count + 1
		endHeight = height + 1
	}

	startKey, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK, startHeight)
	endKey, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK, endHeight)

	iter := sc.db.NewIterator(&util.Range{Start: startKey, Limit: endKey}, nil)

	currentHeight := startHeight
	for i := uint64(0); i < count && iter.Next(); i++ {
		data := iter.Value()
		block := &ledger.SnapshotBlock{}
		if dsErr := block.DbDeserialize(data); dsErr != nil {
			return blocks, dsErr
		}

		if containSnapshotContent {
			snapshotContent, err := sc.GetSnapshotContent(&block.SnapshotHash)
			if err != nil {
				return blocks, err
			}
			block.SnapshotContent = snapshotContent
		}

		block.Hash = *sc.getBlockHash(iter.Key())
		blocks = append(blocks, block)
		currentHeight++
	}

	return blocks, nil
}

func (sc *SnapshotChain) GetSnapshotBlockHeight(snapshotHash *types.Hash) (uint64, error) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCKHASH, snapshotHash)
	data, err := sc.db.Get(key, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return binary.BigEndian.Uint64(data), nil

}

func (sc *SnapshotChain) GetSnapshotBlock(height uint64) (*ledger.SnapshotBlock, error) {
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK, height)

	iter := sc.db.NewIterator(util.BytesPrefix(key), nil)
	defer iter.Release()

	if !iter.Next() {
		if err := iter.Error(); err != nil && err != leveldb.ErrNotFound {
			return nil, err
		}
		return nil, nil
	}

	snapshotBlock := &ledger.SnapshotBlock{}
	if dsErr := snapshotBlock.DbDeserialize(iter.Value()); dsErr != nil {
		return nil, dsErr
	}

	snapshotBlock.Hash = *sc.getBlockHash(iter.Key())
	return snapshotBlock, nil

}

func (sc *SnapshotChain) GetSbHashList(height uint64, count, step int, forward bool) []*types.Hash {
	hashList := make([]*types.Hash, 0)
	key, _ := database.EncodeKey(database.DBKP_SNAPSHOTBLOCK, height)
	iter := sc.db.NewIterator(util.BytesPrefix(key), nil)
	defer iter.Release()

	if forward {
		iter.Next()
	} else {
		iter.Prev()
	}

	for j := 0; j < count; j++ {
		for i := 0; i < step; i++ {
			var ok bool
			if forward {
				ok = iter.Next()
			} else {
				ok = iter.Prev()
			}

			if !ok {
				return hashList
			}
		}

		hashList = append(hashList, sc.getBlockHash(iter.Key()))
	}
	return hashList
}

func (sc *SnapshotChain) GetConfirmAccountBlock(snapshotHeight uint64, address *types.Address) (*ledger.AccountBlock, error) {
	return nil, nil
}