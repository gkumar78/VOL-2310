package log

import (
	"encoding/json"
	"fmt"
	"github.com/opencord/voltha-lib-go/v2/pkg/db/kvstore"
	"github.com/opencord/voltha-lib-go/v2/pkg/db/model"
	"time"
)

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

//create new ConfigManager
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

//populateLogLevel populate the data got from retrive to loglevel 
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

//InitComponentConfig initialize componentconfig for component class,component name and pod name
func InitComponentConfig(componentLabel string, configType ConfigType, createIfNotPresent bool, cm *ConfigManager) (*ComponentConfig, error) {
	//construct ComponentConfig
	//verify componentConfig for provided componentLabel and configType is present in etcd,
	cConfig := &ComponentConfig{
		componentLabel:   componentLabel,
		configType:       configType,
		CManager:         cm,
		monitorEnabled:   createIfNotPresent,
		changeEventChan:  nil,
		kvStoreEventChan: nil,
		logLevel:         nil,
	}

	//check the pod is present already in etcd or not
	if !cConfig.monitorEnabled {
		data, err := cConfig.Retreive()
		if err != nil {
			log.Error("error", err)
			return cConfig, err
		}

		if data != nil {
			loglevel := populateLogLevel(data)
			cConfig.logLevel = loglevel
		}
	}


	return cConfig, nil
}

type ComponentConfig struct {
	CManager         *ConfigManager
	componentLabel   string
	configType       ConfigType
	monitorEnabled   bool
	changeEventChan  chan *ConfigChangeEvent
	kvStoreEventChan chan *kvstore.Event
	logLevel         []LogLevel
}

type LogLevel struct {
	PackageName string
	Level       string
}

func (c *ComponentConfig) MonitorForConfigChange() (chan *ConfigChangeEvent, error) {
	//componentLabel is e.g adapter,open-olt-adapter
	//confiType is e.g. loglevel

	//
	//call makeConfigPath function to create path
	key := c.makeConfigPath()

	//call backend createwatch method
	if c.kvStoreEventChan == nil {
		c.kvStoreEventChan = make(chan *kvstore.Event)

		c.kvStoreEventChan = c.CManager.backend.CreateWatch(key)

	}

	//call this method as goroutine "processKVStoreWatchEvents"
//	go c.testChange()
	go c.processKVStoreWatchEvents(key)

	return c.changeEventChan, nil
}

func (c *ComponentConfig) processKVStoreWatchEvents(key string) {
	//In a loop process incoming kvstore events and push changes to changeEventChan

	//call defer backend delete watch method
	//	defer c.CManager.backend.DeleteWatch(key, c.kvStoreEventChan)
	if c.changeEventChan == nil {
		c.changeEventChan = make(chan *ConfigChangeEvent)
	}

	for watchResp := range c.kvStoreEventChan {
		configEvent := newChangeEvent(watchResp.EventType, watchResp.Key, watchResp.Value)
		//c.changeEventChan <- configEvent
	}
}
/*
func (c *ComponentConfig) testChange() {
	for {
		time.Sleep(60 * time.Second)
		loglevel := LogLevel{}

		//added below 3 lines for testing
		loglevel.PackageName = "github.com#opencord#voltha-lib-go#v2#pkg#kafka"
		loglevel.Level = "ERROR"

		err := c.Save(loglevel)
	}

}*/

// NewEvent creates a new Event object
func newChangeEvent(EventType int, Key, value interface{}) *ConfigChangeEvent {
	evnt := new(ConfigChangeEvent)
	evnt.ChangeType = EventType
	evnt.Key = Key.(string)
	evnt.Value = value

	return evnt
}

//Retreive the data from etcd 
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

	data, err := c.Retreive()
	if err != nil {
		return "", err
	}
	//convert interface value to string and return
	return fmt.Sprintf("%v", data), nil
}

func (c *ComponentConfig) RetreiveAll() (map[string]*kvstore.KVPair, error) {
	key := c.makeConfigPath()

	data, err := c.CManager.backend.List(key)
	if err != nil {
		return nil, err
	}
	return data, nil
}

//Save saves the data to etcd
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
	key := c.makeConfigPath()

	//delete the config
	err := c.CManager.backend.Delete(key)
	if err != nil {
		return err
	}
	return nil
}

//create etcd path
func (c *ComponentConfig) makeConfigPath() string {
	//construct path
	cType := c.configType.String()
	configPath := defaultkvStoreConfigPath + "/" + c.componentLabel + "/" + cType

	return configPath
}
