// Copyright (C) 2019-2020 Zilliz. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License
// is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
// or implied. See the License for the specific language governing permissions and limitations under the License.

package paramtable

import (
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"go.uber.org/zap"

	config "github.com/xige-16/storage-test/pkg/config"
	"github.com/xige-16/storage-test/pkg/log"
	"github.com/xige-16/storage-test/pkg/util/typeutil"
)

// UniqueID is type alias of typeutil.UniqueID
type UniqueID = typeutil.UniqueID

const (
	DefaultMinioUseIAM        = "false"
	DefaultMinioCloudProvider = "aws"
	DefaultMinioIAMEndpoint   = ""

	DefaultMinioRegion         = ""
	DefaultMinioUseVirtualHost = "false"
	DefaultMinioRequestTimeout = "10000"
)

// Const of Global Config List
func globalConfigPrefixs() []string {
	return []string{"metastore", "localStorage", "etcd", "tikv", "minio", "pulsar", "kafka", "rocksmq", "log", "grpc", "common", "quotaAndLimits"}
}

var defaultYaml = []string{"milvus.yaml"}

// BaseTable the basics of paramtable
type BaseTable struct {
	once   sync.Once
	mgr    *config.Manager
	config *baseTableConfig
}

type baseTableConfig struct {
	configDir       string
	refreshInterval int
	skipRemote      bool
	skipEnv         bool
	yamlFiles       []string
}

type Option func(*baseTableConfig)

func Files(files []string) Option {
	return func(bt *baseTableConfig) {
		bt.yamlFiles = files
	}
}

func Interval(interval int) Option {
	return func(bt *baseTableConfig) {
		bt.refreshInterval = interval
	}
}

func SkipRemote(skip bool) Option {
	return func(bt *baseTableConfig) {
		bt.skipRemote = skip
	}
}

func skipEnv(skip bool) Option {
	return func(bt *baseTableConfig) {
		bt.skipEnv = skip
	}
}

// NewBaseTableFromYamlOnly only used in migration tool.
// Maybe we shouldn't limit the configDir internally.
func NewBaseTableFromYamlOnly(yaml string) *BaseTable {
	return NewBaseTable(Files([]string{yaml}), SkipRemote(true), skipEnv(true))
}

func NewBaseTable(opts ...Option) *BaseTable {
	defaultConfig := &baseTableConfig{
		configDir:       initConfPath(),
		yamlFiles:       defaultYaml,
		refreshInterval: 5,
		skipRemote:      false,
		skipEnv:         false,
	}
	for _, opt := range opts {
		opt(defaultConfig)
	}
	bt := &BaseTable{config: defaultConfig}
	bt.init()
	return bt
}

// init initializes the param table.
// if refreshInterval greater than 0 will auto refresh config from source
func (bt *BaseTable) init() {
	formatter := func(key string) string {
		ret := strings.ToLower(key)
		ret = strings.TrimPrefix(ret, "milvus.")
		ret = strings.ReplaceAll(ret, "/", "")
		ret = strings.ReplaceAll(ret, "_", "")
		ret = strings.ReplaceAll(ret, ".", "")
		return ret
	}
	bt.mgr, _ = config.Init()
	if !bt.config.skipEnv {
		err := bt.mgr.AddSource(config.NewEnvSource(formatter))
		if err != nil {
			log.Warn("init baseTable with env failed", zap.Error(err))
			return
		}
	}
	bt.initConfigsFromLocal()
}

func (bt *BaseTable) initConfigsFromLocal() {
	refreshInterval := bt.config.refreshInterval
	err := bt.mgr.AddSource(config.NewFileSource(&config.FileInfo{
		Files: lo.Map(bt.config.yamlFiles, func(file string, _ int) string {
			return path.Join(bt.config.configDir, file)
		}),
		RefreshInterval: time.Duration(refreshInterval) * time.Second,
	}))
	if err != nil {
		log.Warn("init baseTable with file failed", zap.Strings("configFile", bt.config.yamlFiles), zap.Error(err))
		return
	}
}

// GetConfigDir returns the config directory
func (bt *BaseTable) GetConfigDir() string {
	return bt.config.configDir
}

func initConfPath() string {
	// check if user set conf dir through env
	configDir := os.Getenv("MILVUSCONF")
	if len(configDir) != 0 {
		return configDir
	}
	runPath, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	configDir = runPath + "/configs"
	if _, err := os.Stat(configDir); err != nil {
		_, fpath, _, _ := runtime.Caller(0)
		configDir = path.Dir(fpath) + "/../../../configs"
	}
	return configDir
}

func (bt *BaseTable) FileConfigs() map[string]string {
	return bt.mgr.FileConfigs()
}

func (bt *BaseTable) UpdateSourceOptions(opts ...config.Option) {
	bt.mgr.UpdateSourceOptions(opts...)
}

// Load loads an object with @key.
func (bt *BaseTable) Load(key string) (string, error) {
	return bt.mgr.GetConfig(key)
}

func (bt *BaseTable) Get(key string) string {
	return bt.GetWithDefault(key, "")
}

// GetWithDefault loads an object with @key. If the object does not exist, @defaultValue will be returned.
func (bt *BaseTable) GetWithDefault(key, defaultValue string) string {
	if bt.mgr == nil {
		return defaultValue
	}

	str, err := bt.mgr.GetConfig(key)
	if err != nil {
		return defaultValue
	}
	return str
}

// Remove Config by key
func (bt *BaseTable) Remove(key string) error {
	bt.mgr.DeleteConfig(key)
	return nil
}

// Update Config
func (bt *BaseTable) Save(key, value string) error {
	bt.mgr.SetConfig(key, value)
	return nil
}

func (bt *BaseTable) SaveGroup(group map[string]string) error {
	for key, value := range group {
		bt.mgr.SetMapConfig(key, value)
	}
	return nil
}

// Reset Config to default value
func (bt *BaseTable) Reset(key string) error {
	bt.mgr.ResetConfig(key)
	return nil
}
