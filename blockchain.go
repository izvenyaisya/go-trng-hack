package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
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
	// in-memory signing key for tier signatures (HMAC-SHA256)
	signingKey []byte
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

// initSigningKey ensures we have an in-memory signing key; if nil, generate one.
func initSigningKey() {
	if len(signingKey) == 0 {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		signingKey = k
	}
}

// signTierPayload computes HMAC-SHA256 over provided payload
func signTierPayload(payload []byte) string {
	if len(signingKey) == 0 {
		initSigningKey()
	}
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(payload)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

// persistence
type persistedStore struct {
	TxStore map[string]*Transaction `json:"tx_store"`
	Chain   []Block                 `json:"chain"`
	// signing key stored as hex; if SIGNING_KEY_PASSPHRASE set at runtime then the value
	// will be AES-GCM encrypted hex (nonce + ciphertext) and should be decrypted on load.
	SigningKey string `json:"signing_key,omitempty"`
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

	// persist signing key: encode as hex or encrypted hex if passphrase provided
	skHex := ""
	if len(signingKey) > 0 {
		pass := os.Getenv("SIGNING_KEY_PASSPHRASE")
		if pass != "" {
			if enc, err := encryptWithPassphrase(signingKey, pass); err == nil {
				skHex = enc
			} else {
				// fallback to raw hex if encryption fails
				skHex = hex.EncodeToString(signingKey)
			}
		} else {
			skHex = hex.EncodeToString(signingKey)
		}
	}
	p := persistedStore{TxStore: copyTx, Chain: copyChain, SigningKey: skHex}
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

	// restore signing key if present
	if p.SigningKey != "" {
		pass := os.Getenv("SIGNING_KEY_PASSPHRASE")
		var sk []byte
		var err error
		if pass != "" {
			// try decrypt with passphrase
			sk, err = decryptWithPassphrase(p.SigningKey, pass)
			if err != nil {
				// fallback: try raw hex decode
				if b, err2 := hex.DecodeString(p.SigningKey); err2 == nil {
					sk = b
					err = nil
				}
			}
		} else {
			sk, err = hex.DecodeString(p.SigningKey)
		}
		if err == nil && len(sk) > 0 {
			signingKey = sk
			log.Printf("restored signing key from store")
		} else {
			log.Printf("failed to restore signing key from store: %v", err)
		}
	}
	log.Printf("loaded store: %d transactions, %d blocks", len(txStore), len(chain))
	return nil
}

// deriveKeyFromPassphrase: SHA256(passphrase)
func deriveKeyFromPassphrase(pass string) []byte {
	h := sha256.Sum256([]byte(pass))
	return h[:]
}

// encryptWithPassphrase encrypts raw bytes with AES-GCM and returns hex(nonce|ciphertext)
func encryptWithPassphrase(raw []byte, pass string) (string, error) {
	key := deriveKeyFromPassphrase(pass)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := g.Seal(nil, nonce, raw, nil)
	out := append(nonce, ct...)
	return hex.EncodeToString(out), nil
}

// decryptWithPassphrase decodes hex(nonce|ciphertext) and decrypts with AES-GCM
func decryptWithPassphrase(hexIn string, pass string) ([]byte, error) {
	data, err := hex.DecodeString(hexIn)
	if err != nil {
		return nil, err
	}
	key := deriveKeyFromPassphrase(pass)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := g.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := data[:ns]
	ct := data[ns:]
	pt, err := g.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	return pt, nil
}
