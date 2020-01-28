package log

import (
	"encoding/json"
	"fmt"
	"github.com/opencord/voltha-lib-go/v2/pkg/db/kvstore"
	"github.com/opencord/voltha-lib-go/v2/pkg/db/model"
	//	"github.com/opencord/voltha-lib-go/v2/pkg/log"
)

/*func init() {
	_, err := log.AddPackage(log.JSON, log.DebugLevel, nil)
	if err != nil {
		log.Errorw("unable-to-register-package-to-the-log-map", log.Fields{"error": err})
	}
}
*/
const (
	defaultkvStoreConfigPath = "config"
)

type ConfigType int

const (
	ConfigTypeLogLevel ConfigType = iota
	ConfigTypeKafka
)

func (c ConfigType) String() string {
	return [...]string{"loglevel", "kafka"}[c]
}

type ChangeEventType int

const (
	Put ChangeEventType = iota
	Delete
)

func (c ChangeEventType) EventString() string {
	return [...]string{"Put", "Delete"}[c]
}

type ConfigChangeEvent struct {
	ChangeType int
	Key        string
	Value      interface{}
}

type ConfigManager struct {
	backend *model.Backend
}

//Create New ConfigManager
func NewConfigManager(kvClient kvstore.Client, kvStoreType, kvStoreHost, kvStoreDataPrefix string, kvStorePort, kvStoreTimeout int) *ConfigManager {
	// Setup the KV store
	var cm ConfigManager
	cm.backend = &model.Backend{
		Client:     kvClient,
		StoreType:  kvStoreType,
		Host:       kvStoreHost,
		Port:       kvStorePort,
		Timeout:    kvStoreTimeout,
		PathPrefix: kvStoreDataPrefix}
	return &cm
}

//populateLogLevel store the loglevel retrieved from kvstore to LogLevel
func populateLogLevel(data map[string]interface{}) []LogLevel {
	loglevel := []LogLevel{}
	for _, v := range data {
		switch val := v.(type) {
		case []byte:
			json.Unmarshal(val, &loglevel)
		}
	}

	return loglevel
}

//Initialize the component config
func InitComponentConfig(componentLabel string, configType ConfigType, cm *ConfigManager) (*ComponentConfig, error) {
	//construct ComponentConfig
	//verify componentConfig for provided componentLabel and configType is present in etcd,

	cConfig := &ComponentConfig{
		componentLabel:   componentLabel,
		configType:       configType,
		CManager:         cm,
		changeEventChan:  nil,
		kvStoreEventChan: nil,
		logLevel:         nil,
	}

	return cConfig, nil
}

type ComponentConfig struct {
	CManager         *ConfigManager
	componentLabel   string
	configType       ConfigType
	changeEventChan  chan *ConfigChangeEvent
	kvStoreEventChan chan *kvstore.Event
	logLevel         []LogLevel
}

type LogLevel struct {
	PackageName string
	Level       string
}

//MonitorForConfigChange watch on the keys
//If any changes happen then process the event create new ConfigChangeEvent and return
func (c *ComponentConfig) MonitorForConfigChange() (chan *ConfigChangeEvent, error) {
	//call makeConfigPath function to create path
	key := c.makeConfigPath()

	//call backend createwatch method
	c.kvStoreEventChan = make(chan *kvstore.Event)
	c.changeEventChan = make(chan *ConfigChangeEvent)

	c.kvStoreEventChan = c.CManager.backend.CreateWatch(key)

	go c.processKVStoreWatchEvents(key)

	return c.changeEventChan, nil
}

//processKVStoreWatchEvents process the kvStoreEventChan and create ConfigChangeEvent
func (c *ComponentConfig) processKVStoreWatchEvents(key string) {
	//In a loop process incoming kvstore events and push changes to changeEventChan

	//call defer backend delete watch method

	for watchResp := range c.kvStoreEventChan {
		configEvent := newChangeEvent(watchResp.EventType, watchResp.Key, watchResp.Value)
		c.changeEventChan <- configEvent
	}
}

// NewEvent creates a new Event object
func newChangeEvent(eventType int, key, value interface{}) *ConfigChangeEvent {
	//var key string
	evnt := new(ConfigChangeEvent)
	evnt.ChangeType = eventType
	evnt.Key = fmt.Sprintf("%s", key)
	evnt.Value = value

	return evnt
}

func (c *ComponentConfig) Retreive() (map[string]interface{}, error) {
	//retrieve the data for componentLabel,configType and configKey
	//e.g. componentLabel "adapter",configType "loglevel" and configKey "config"

	//construct key using makeConfigPath
	key := c.makeConfigPath()

	//perform get operation on backend using constructed key
	data, err := c.CManager.backend.Get(key)
	if err != nil || data == nil {
		return nil, err
	}

	res := make(map[string]interface{})
	if data.Value != nil {
		res[data.Key] = data.Value

		dat := populateLogLevel(res)
		return res, nil
	}
	return nil, nil

}

func (c *ComponentConfig) RetreiveAsString() (string, error) {
	//call  Retrieve method
	c.CManager.backend.Lock()
	defer c.CManager.backend.Unlock()

	data, err := c.Retreive()
	if err != nil {
		return "", err
	}
	//convert interface value to string and return
	return fmt.Sprintf("%v", data), nil
}

func (c *ComponentConfig) RetreiveAll() (map[string]*kvstore.KVPair, error) {
	c.CManager.backend.Lock()
	defer c.CManager.backend.Unlock()
	key := c.makeConfigPath()

	data, err := c.CManager.backend.List(key)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (c *ComponentConfig) Save(configValue interface{}) error {
	//construct key using makeConfigPath
	key := c.makeConfigPath()

	configVal, err := json.Marshal(configValue)
	if err != nil {
		log.Error(err)
	}

	//save the data for update config
	err = c.CManager.backend.Put(key, configVal)
	if err != nil {
		return err
	}
	return nil
}

func (c *ComponentConfig) Delete(configKey string) error {
	//construct key using makeConfigPath
	c.CManager.backend.Lock()
	defer c.CManager.backend.Unlock()
	key := c.makeConfigPath()

	//delete the config
	err := c.CManager.backend.Delete(key)
	if err != nil {
		return err
	}
	return nil
}

//crete key
func (c *ComponentConfig) makeConfigPath() string {
	//construct path
	cType := c.configType.String()
	configPath := defaultkvStoreConfigPath + "/" + c.componentLabel + "/" + cType
	return configPath
}
