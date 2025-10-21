package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	txStore    = map[string]*Transaction{}
	txMutex    sync.RWMutex
	chain      = make([]Block, 0)
	chainMutex sync.RWMutex
)

func appendBlock(tx *Transaction) {
	chainMutex.Lock()
	prev := ""
	if len(chain) > 0 {
		prev = chain[len(chain)-1].Hash
	}
	blk := Block{
		Index:     len(chain),
		Timestamp: time.Now().Unix(),
		TxID:      tx.TxID,
		DataHash:  tx.Published,
		PrevHash:  prev,
	}
	blk.Hash = computeBlockHash(blk)
	chain = append(chain, blk)
	// unlock before persisting because saveStore acquires chainMutex.RLock
	chainMutex.Unlock()

	// persist store after new block appended
	if err := saveStore(); err != nil {
		// log but don't fail the HTTP request
		log.Printf("failed to save store: %v", err)
	} else {
		log.Printf("appended block %d (tx=%s) and saved store", blk.Index, tx.TxID)
	}
}

func computeBlockHash(b Block) string {
	s := fmt.Sprintf("%d:%d:%s:%s:%s", b.Index, b.Timestamp, b.TxID, b.DataHash, b.PrevHash)
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func validateChain() bool {
	chainMutex.RLock()
	defer chainMutex.RUnlock()
	for i := range chain {
		if computeBlockHash(chain[i]) != chain[i].Hash {
			return false
		}
		if i > 0 && chain[i].PrevHash != chain[i-1].Hash {
			return false
		}
	}
	return true
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// persistence
type persistedStore struct {
	TxStore map[string]*Transaction `json:"tx_store"`
	Chain   []Block                 `json:"chain"`
}

func storePath() string {
	// store.json in current working directory
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, "store.json")
}

func saveStore() error {
	// copy under locks
	txMutex.RLock()
	copyTx := make(map[string]*Transaction, len(txStore))
	for k, v := range txStore {
		// shallow copy the transaction value but sanitize the Simulation to avoid
		// marshalling potentially very large simulation paths into store.json
		tmp := *v
		tmp.Sim = SimulationData{}
		copyTx[k] = &tmp
	}
	txMutex.RUnlock()

	chainMutex.RLock()
	copyChain := make([]Block, len(chain))
	copy(copyChain, chain)
	chainMutex.RUnlock()

	p := persistedStore{TxStore: copyTx, Chain: copyChain}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := storePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, storePath()); err != nil {
		return err
	}
	log.Printf("store persisted to %s", storePath())
	return nil
}

func loadStore() error {
	path := storePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var p persistedStore
	if err := json.Unmarshal(b, &p); err != nil {
		// backup corrupt store and return an error so the caller knows load failed
		ts := time.Now().Format("20060102-150405")
		bad := path + ".corrupt-" + ts
		if err2 := os.Rename(path, bad); err2 == nil {
			log.Printf("loadStore: moved invalid store.json to %s due to parse error: %v", bad, err)
		} else {
			log.Printf("loadStore: failed to move invalid store.json: %v (parse error: %v)", err2, err)
			// return the original parse error if we couldn't move the file
			return err
		}
		return fmt.Errorf("store.json invalid, moved to %s: %w", bad, err)
	}
	txMutex.Lock()
	txStore = p.TxStore
	txMutex.Unlock()
	chainMutex.Lock()
	chain = p.Chain
	chainMutex.Unlock()
	log.Printf("loaded store: %d transactions, %d blocks", len(txStore), len(chain))
	return nil
}
