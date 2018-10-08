package config

import (
	"encoding/json"
	"github.com/vitelabs/go-vite/common"
	"github.com/vitelabs/go-vite/log15"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	*P2P    `json:"P2P"`
	*Miner  `json:"Miner"`
	*Ledger `json:"Ledger"`

	// global keys
	DataDir string `json:"DataDir"`
}

func (c Config) RunLogDir() string {
	return filepath.Join(c.DataDir, "runlog")
}

func (c Config) RunLogDirFile() (string, error) {
	filename := time.Now().Format("2006-01-02") + ".log"
	if err := os.MkdirAll(c.RunLogDir(), 0777); err != nil {
		return "", err
	}
	return filepath.Join(c.RunLogDir(), filename), nil

}

var log = log15.New("module", "config")

const configFileName = "vite.config.json"

var GlobalConfig *Config

func defaultConfig() {
	GlobalConfig = &Config{
		P2P: &P2P{
			Name:                 "vite-server",
			PrivateKey:           "",
			MaxPeers:             100,
			MaxPassivePeersRatio: 2,
			MaxPendingPeers:      20,
			BootNodes:            nil,
			Addr:                 "0.0.0.0:8483",
			Datadir:              common.DefaultDataDir(),
			NetID:                6,
		},
		Miner: &Miner{
			Miner:         false,
			Coinbase:      "",
			MinerInterval: 6,
		},
		Ledger: &Ledger{
			IsDownload:         true, // Default download ledger zip
			ResendGenesisBlock: false,
		},
		DataDir: common.DefaultDataDir(),
	}
}

func init() {
	defaultConfig()

	if text, err := ioutil.ReadFile(configFileName); err == nil {
		err = json.Unmarshal(text, GlobalConfig)
		if err != nil {
			log.Info("cannot unmarshal the config file content, will use the default config", "error", err)
		}
	} else {
		log.Info("cannot read the config file, will use the default config", "error", err)
	}

	// set default value global keys
	if GlobalConfig.DataDir == "" {
		GlobalConfig.DataDir = common.DefaultDataDir()
	}
}
