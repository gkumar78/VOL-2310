package log

import (
	"crypto/md5"
	"encoding/json"
	"github.com/opencord/voltha-lib-go/v2/pkg/log"
	"os"
	"strings"
	"time"
)

type ComponentLogController struct {
	ComponentName       string
	componentNameConfig *ComponentConfig
	globalConfig        *ComponentConfig
	configManager       *ConfigManager
	logHash             [16]byte
	hashActive          bool
}

//create new component LogController
func NewComponentLogController(cm *ConfigManager) (*ComponentLogController, error) {
	//populate the ComponentLogController with env variables
	componentName := os.Getenv("COMPONENTNAME")
	hash := [16]byte{}

	cc := &ComponentLogController{
		ComponentName:       componentName,
		componentNameConfig: nil,
		globalConfig:        nil,
		configManager:       cm,
		logHash:             hash,
		hashActive:          false}

	return cc, nil
}

//ProcessLogConfigChange initialize component config and global config then process the log config changes
func ProcessLogConfigChange(cm *ConfigManager) {
	//populate the ComponentLogController using NewComponentLogController

	cc, err := NewComponentLogController(cm)
	if err != nil {
		log.Error("error", err)
	}

	ccGlobal, err := InitComponentConfig("global", ConfigTypeLogLevel, cm)
	if err != nil {
		log.Errorw("fail-to-create-component-config", log.Fields{"error": err})
	}
	cc.globalConfig = ccGlobal

	ccComponentName, err := InitComponentConfig(cc.ComponentName, ConfigTypeLogLevel, cm)
	if err != nil {
		log.Errorw("fail-to-create-component-config", log.Fields{"error": err})
	}
	cc.componentNameConfig = ccComponentName

	//call ProcessConfigChange and check for any changes to config
	cc.processLogConfig()
}

//processLogConfig wait on componentn config and global config channel for any changes
//If any changes happen in kvstore then process the changes and update the log config
//then load and apply the config for the component
func (c *ComponentLogController) processLogConfig() {
	configPath := "service/voltha/config/"
	logPath := "/loglevel"
	//call MonitorForConfigChange  for componentNameConfig
	//get ConfigChangeEvent Channel for componentName
	changeInComponentNameConfigEvent, _ := c.componentNameConfig.MonitorForConfigChange()

	//call MonitorForConfigChange  for global
	//get ConfigChangeEvent Channel for global
	changeInglobalConfigEvent, _ := c.globalConfig.MonitorForConfigChange()

	changeEvent := &ConfigChangeEvent{}
	configEvent := &ConfigChangeEvent{}

	//process the events for componentName and  global config
	for {
		select {
		case configEvent = <-changeInglobalConfigEvent:

		case configEvent = <-changeInComponentNameConfigEvent:

		}
		changeEvent = configEvent
		time.Sleep(5 * time.Second)

		//if the eventType is Put call updateLogConfig for  componentName or global config
		if changeEvent.ChangeType == 0 {
			component := getComponentName(changeEvent.Key, configPath, logPath)
			packageName, logLevel := getEventChangeData(changeEvent.Value)
			c.updateLogConfig(packageName, logLevel, component)

			//loadAndApplyLogConfig
			c.loadAndApplyLogConfig()
		}

	}

	//if the eventType is Delete call propagateLogConfigForClear
	//need to discuss on loading after delete

}

//get the packagename and loglevel changed for the event
func getEventChangeData(data interface{}) (string, string) {
	var pName, level string
	loglevel := []LogLevel{}
	json.Unmarshal(data.([]byte), &loglevel)

	for _, v := range loglevel {
		pName = v.PackageName
		level = v.Level
	}
	return pName, level
}

//get component type from the Key for which event has changed
func getComponentName(value string, configPath string, logPath string) string {
	// Get substring between two strings.
	posFirst := strings.Index(value, configPath)
	if posFirst == -1 {
		return ""
	}
	posLast := strings.Index(value, logPath)
	if posLast == -1 {
		return ""
	}
	posFirstAdjusted := posFirst + len(configPath)
	if posFirstAdjusted >= posLast {
		return ""
	}
	return value[posFirstAdjusted:posLast]
}

//get active loglevel from the zap logger
func getActiveLogLevel() ([]LogLevel, error) {
	loglevel := LogLevel{}
	logLevels := []LogLevel{}

	// now do the default log level
	loglevel.PackageName = "default"
	loglevel.Level = log.IntToString(log.GetDefaultLogLevel())

	logLevels = append(logLevels, loglevel)

	// do the per-package log levels
	for _, packageName := range log.GetPackageNames() {
		level, err := log.GetPackageLogLevel(packageName)
		if err != nil {
			return nil, err
		}

		packagename := strings.ReplaceAll(packageName, "/", "#")
		loglevel.PackageName = packagename
		loglevel.Level = log.IntToString(level)

		logLevels = append(logLevels, loglevel)
	}

	return logLevels, nil
}

//update the component config if the component config is not present in store
func (c *ComponentLogController) updateLogConfigToComponent(componentConfig, globalConfig *ComponentConfig) error {
	//get list of all the keys for the component config.If  component config is not set then retrieve global config and update the component  config

	componentData, err := componentConfig.Retreive()
	if err != nil {
		log.Error(err)
		return err
	}
	loglevel := populateLogLevel(componentData)
	componentConfig.logLevel = loglevel

	if componentData == nil {
		globalData, err := globalConfig.Retreive()
		if err != nil {
			log.Error(err)
			return err
		}
		globalloglevel := populateLogLevel(globalData)
		globalConfig.logLevel = globalloglevel

		componentConfig.logLevel = globalConfig.logLevel
	}

	return nil
}

//updateComponentLogLevel retrieve the loglevel for the component config from the kvstore then update the  package loglevel with
//changed loglevel
func (c *ComponentLogController) updateComponentLogLevel(componentConfig *ComponentConfig, packageName, loglevel string) {
	data, err := componentConfig.Retreive()
	if err != nil {
		log.Error(err)
	}
	logLevel := populateLogLevel(data)
	componentConfig.logLevel = logLevel

	for componentIndex, componentLevel := range componentConfig.logLevel {
		if componentLevel.PackageName == packageName {
			componentConfig.logLevel[componentIndex].Level = loglevel
		}
	}
}

//updateLogConfig check the component type is global or component name then update the component config
func (c *ComponentLogController) updateLogConfig(packageName, logLevel, component string) {
	//call updateLogConfigToComponent for global to componentname
	//if default loglevel for global is set and default for component name is not set

	if packageName == "default" && component == "global" {
		err := c.updateLogConfigToComponent(c.componentNameConfig, c.globalConfig)
		if err != nil {
			log.Error(err)
		}
	}

	//call updateComponentLogLevel if any changes happen to default or package loglevel for component config
	if component == c.ComponentName {
		c.updateComponentLogLevel(c.componentNameConfig, packageName, logLevel)
	}

}

func (c *ComponentLogController) loadAndApplyLogConfig() {
	//load the current configuration for component name using Retreive method
	//create hash of loaded configuration using GenerateLogConfigHash
	//if there is previous hash stored, compare the hash to stored hash
	//if there is any change will call UpdateLogLevels

	//generate hash
	currentLogHash := GenerateLogConfigHash(c.componentNameConfig.logLevel)

	//will set the activeHash to true and update the logHash
	if c.logHash != currentLogHash {
		UpdateLogLevels(c.componentNameConfig.logLevel)
		c.hashActive = true
		c.logHash = currentLogHash
	}

}

//call getDefaultLogLevel to get active default log level
func getDefaultLogLevel(logLevel []LogLevel) string {

	for _, level := range logLevel {
		if level.PackageName == "default" {
			return level.Level
		}
	}
	return ""
}

//create loglevel to set loglevel for the component
func createCurrentLogLevel(activeLogLevels, currentLogLevel []LogLevel) []LogLevel {

	level := getDefaultLogLevel(currentLogLevel)
	for activeIndex, activeLevel := range activeLogLevels {
		exist := false
		for currentLogLevelIndex, _ := range currentLogLevel {
			if activeLogLevels[activeIndex].PackageName == currentLogLevel[currentLogLevelIndex].PackageName {
				exist = true
				break
			}
		}
		if !exist {
			if level != "" {
				activeLevel.Level = level
			}
			currentLogLevel = append(currentLogLevel, activeLevel)
		}
	}
	return currentLogLevel
}

//updateLogLevels update the loglevels for the component
func UpdateLogLevels(logLevel []LogLevel) {
	//it should retrieve active confguration from logger
	//it should compare with new entries one by one and apply if any changes

	activeLogLevels, _ := getActiveLogLevel()
	currentLogLevel := createCurrentLogLevel(activeLogLevels, logLevel)
	for _, level := range currentLogLevel {
		if level.PackageName == "default" {
			log.SetDefaultLogLevel(log.StringToInt(level.Level))
		} else {
			pname := strings.ReplaceAll(level.PackageName, "#", "/")
			log.SetPackageLogLevel(pname, log.StringToInt(level.Level))
		}
	}
}

//generate hash to check for any changes happened to current and active loglevel
func GenerateLogConfigHash(createHashLog []LogLevel) [16]byte {
	//it will generate md5 hash of key value pairs appended into a single string
	//in order by key name
	createHashLogBytes := []byte{}
	for _, level := range createHashLog {
		levelData, _ := json.Marshal(level)
		createHashLogBytes = append(createHashLogBytes, levelData...)
	}
	return md5.Sum(createHashLogBytes)
}
