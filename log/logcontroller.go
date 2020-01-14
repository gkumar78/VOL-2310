package log

import (
	"crypto/md5"
	"encoding/json"
	"github.com/opencord/voltha-lib-go/v2/pkg/log"
	"os"
	"strings"
)

type PodLogController struct {
	ComponentClass       string
	componentClassConfig *ComponentConfig
	ComponentName        string
	componentNameConfig  *ComponentConfig
	PodName              string
	podNameConfig        *ComponentConfig
	configManager        *ConfigManager
	logHash              [16]byte
	hashActive           bool
}

//create new PodController
func NewPodLogController(cm *ConfigManager) (*PodLogController, error) {
	//populate the PodLogController with env variables
	podName := os.Getenv("HOSTNAME")
	componentClassName := os.Getenv("COMPONENTCLASSNAME")
	componentName := os.Getenv("COMPONENTNAME")

	hash := [16]byte{}

	pc := &PodLogController{
		ComponentClass:       componentClassName,
		ComponentName:        componentName,
		PodName:              podName,
		podNameConfig:        nil,
		componentNameConfig:  nil,
		componentClassConfig: nil,
		configManager:        cm,
		logHash:              hash,
		hashActive:           false}

	return pc, nil
}

//ProcessInitialLogConfig load the initial log config for pod and then start watching on etcd keys 
func ProcessInitialLogConfig(cm *ConfigManager, loglevel string) {
	//populate the PodLogController using NewPodLogController
	var packageName, logLevel string
	pc, err := NewPodLogController(cm)
	if err != nil {
		log.Error("error", err)
	}

	//call InitComponentConfig method to construct instance of  ComponentConfig for pod name with createIfNotPresent=false
	ccPodName, err := InitComponentConfig(pc.PodName, ConfigTypeLogLevel, false, cm)
	if err != nil {
		log.Errorw("fail-to-create-component-config", log.Fields{"error": err})
	}

	pc.podNameConfig = ccPodName

	//if the componentConfig for podname is not present then call persistInitialLogConfig
	//if the componentConfig for podname is present proceed further

	if pc.podNameConfig.logLevel == nil {
		err := pc.persistInitialLogConfig()
		if err != nil {
			log.Error(err)
		}
	}

	//call InitComponentConfig method to construct instance of  ComponentConfig one each for component class
	//component name and pod name with createIfNotPresent=true
	ccComponentClass, err := InitComponentConfig(pc.ComponentClass, ConfigTypeLogLevel, true, cm)
	if err != nil {
		log.Errorw("fail-to-create-component-config", log.Fields{"error": err})
	}
	pc.componentClassConfig = ccComponentClass

	ccComponentName, err := InitComponentConfig(pc.ComponentName, ConfigTypeLogLevel, true, cm)
	if err != nil {
		log.Errorw("fail-to-create-component-config", log.Fields{"error": err})
	}
	pc.componentNameConfig = ccComponentName

	//call initialiseComponentKeys
	err = pc.initialiseComponentKeys(loglevel)
	if err != nil {
		log.Error(err)
	}

	//call propagateLogConfig
	pc.propagateLogConfig(packageName, logLevel, pc.ComponentClass)

	//load pod config using loadAndApplyLogConfig
	pc.loadAndApplyLogConfig()

	//call ProcessConfigChange and check for any changes to config
	pc.ProcessConfigChange()
}

//ProcessConfigChange watches on componentclass,componentname and podname config changes
func (p *PodLogController) ProcessConfigChange() {
	//call MonitorForConfigChange  for componentClassConfig
	//get ConfigChangeEvent Channel for componentClass

	changeInComponentClassConfigEvent, _ := p.componentClassConfig.MonitorForConfigChange()

	//call MonitorForConfigChange  for componentNameConfig
	//get ConfigChangeEvent Channel for componentName
	changeInComponentNameConfigEvent, _ := p.componentNameConfig.MonitorForConfigChange()

	//call MonitorForConfigChange  for podNameConfig
	//get ConfigChangeEvent Channel for podName
	changeInPodNameConfigEvent, _ := p.podNameConfig.MonitorForConfigChange()

	//process the events for componentClass, componentName and podName
	changeEvent := &ConfigChangeEvent{}
	for {
		select {
		case classConfigEvent := <-changeInComponentClassConfigEvent:
			changeEvent = classConfigEvent
		case nameConfigEvent := <-changeInComponentNameConfigEvent:
			changeEvent = nameConfigEvent
		case podNameConfigEvent := <-changeInPodNameConfigEvent:
			changeEvent = podNameConfigEvent

		}
	}

	//if the eventType is Put call propagateLogConfig for componentClass, componentName or podName
	if changeEvent.ChangeType == 0 {
		configPath := "/service/voltha/config/"
		logPath := "/loglevel"
		component := getComponentName(changeEvent.Key, configPath, logPath)
		packageName, logLevel := getEventChangeData(changeEvent.Value)
		p.propagateLogConfig(packageName, logLevel, component)
	}

	//loadAndApplyLogConfig
	p.loadAndApplyLogConfig()

	//if the eventType is Delete call propagateLogConfigForClear
	//need to discuss on loading after delete

}

//getEventChangeData process event data and returns packagename and level 
func getEventChangeData(data interface{}) (string, string) {
	loglevel := LogLevel{}
	json.Unmarshal(data.([]byte), &loglevel)

	return loglevel.PackageName, loglevel.Level
}

//getComponentName process the etcd key got from event and returns the component name
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

//saveDefaultLogLevel get all active default and package loglevel from zap logger
func (p *PodLogController) saveDefaultLogLevel() (interface{}, error) {
	//check for default pod loglevel from log package and save using podNameConfig
	loglevel := LogLevel{}

	// now do the default log level
	loglevel.PackageName = "default"
	loglevel.Level = log.IntToString(log.GetDefaultLogLevel())

	p.podNameConfig.logLevel = append(p.podNameConfig.logLevel, loglevel)

	// do the per-package log levels
	for _, packageName := range log.GetPackageNames() {
		level, err := log.GetPackageLogLevel(packageName)
		if err != nil {
			return nil, err
		}

		packagename := strings.ReplaceAll(packageName, "/", "#")
		loglevel.PackageName = packagename
		loglevel.Level = log.IntToString(level)

		p.podNameConfig.logLevel = append(p.podNameConfig.logLevel, loglevel)
	}

	return p.podNameConfig.logLevel, nil
}

func (p *PodLogController) persistInitialLogConfig() error {
	//check for podNameConfig using  ;if found return podnameConfig
	//not get log default loglevel and package loglevel using
	//saveDefaultLogLevel and save to the store using Save
	data, err := p.saveDefaultLogLevel()
	if err != nil {
		return err
	}
	err = p.podNameConfig.Save(data)
	if err != nil {
		return err
	}

	return nil
}

func (p *PodLogController) initialiseComponentKeys(logLevel string) error {
	//check for componentClassConfig from  using Retreive method;
	//if not found use componentClassConfig and push default(got it from helm) loglevel to using Save
	data, err := p.componentClassConfig.Retreive()

	if err != nil {
		return err
	}

	if data == nil {
		loglevel := LogLevel{}

		loglevel.PackageName = "default"
		loglevel.Level = logLevel
		p.componentClassConfig.logLevel = append(p.componentClassConfig.logLevel, loglevel)

		err = p.componentClassConfig.Save(p.componentClassConfig.logLevel)
		if err != nil {
			return err
		}
	}

	//check for componentName from ConfigManager using Retreive method;
	//if not found use componentNameConfig and push default(got it from helm) loglevel to ConfigManager using Save
	data, err = p.componentNameConfig.Retreive()
	if err != nil {
		return err
	}

	if data == nil {
		loglevel := LogLevel{}

		loglevel.PackageName = "default"
		loglevel.Level = logLevel
		p.componentNameConfig.logLevel = append(p.componentNameConfig.logLevel, loglevel)

		err = p.componentNameConfig.Save(p.componentNameConfig.logLevel)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *PodLogController) propagateLogConfigToChild(parentComponentConfig, childComponentConfig *ComponentConfig) {
	//get list of all the keys for the parentComponentLabel using Retrieve method and store the same keys for childComponentLabel
	//using Save method

	data, err := parentComponentConfig.Retreive()
	if err != nil {
		log.Error(err)
	}
	loglevel := populateLogLevel(data)
	parentComponentConfig.logLevel = loglevel

	childdata, err := childComponentConfig.Retreive()
	if err != nil {
		log.Error(err)
	}
	childloglevel := populateLogLevel(childdata)
	childComponentConfig.logLevel = childloglevel

	addLogLevelToChild(childComponentConfig, parentComponentConfig)
	progatelog(childComponentConfig, parentComponentConfig)

	err = childComponentConfig.Save(childComponentConfig.logLevel)
	if err != nil {
		log.Error(err)
	}
}


//propogatelog propogates loglevel from parentcomponent to childcomponent for packages as well as for default
func progatelog(childComponentConfig, parentComponentConfig *ComponentConfig) {
	for parentindex, parentLevel := range parentComponentConfig.logLevel {
		for childindex, childLevel := range childComponentConfig.logLevel {
			if parentComponentConfig.logLevel[parentindex].PackageName == childComponentConfig.logLevel[childindex].PackageName {
				childComponentConfig.logLevel[childindex].Level = parentLevel.Level
			}
		}
	}
}

//addLogLevelToChild add packages from parentcomponent to childcomponent if the package is not present in childcomponent
func addLogLevelToChild(childComponentConfig, parentComponentConfig *ComponentConfig) {
	for parentindex, parentLevel := range parentComponentConfig.logLevel {
		exist := false
		for childindex, _ := range childComponentConfig.logLevel {
			if parentComponentConfig.logLevel[parentindex].PackageName == childComponentConfig.logLevel[childindex].PackageName {
				exist = true
				break
			}
		}
		if !exist {
			childComponentConfig.logLevel = append(childComponentConfig.logLevel, parentLevel)
		}
	}
}

//propagateLogConfig propagates loglevels for default and pacakge specific at the time of pod start and
//if any changes happens  in etcd 
func (p *PodLogController) propagateLogConfig(packageName, logLevel, component string) {
	//call propagateLogConfigToChild for componnent class to name
	//if default loglevel for componentclass is set then call updateAllLogLevelsOfComponent for component class

	if packageName == "default" {
		p.updateAllLogLevelsOfComponent(p.componentClassConfig, logLevel)
	} else if component == p.ComponentClass {
		p.propagateLogConfigToChild(p.componentClassConfig, p.componentNameConfig)
	}

	//call propagateLogConfigToChild for componnent name to pod name
	//if default loglevel for componentname is set then call updateAllLogLevelsOfComponent for component name
	if packageName == "default" {
		p.updateAllLogLevelsOfComponent(p.componentNameConfig, logLevel)
	} else if component == p.ComponentName || component == p.ComponentClass {
		p.propagateLogConfigToChild(p.componentNameConfig, p.podNameConfig)
	}

	//if default loglevel for componentname is set then call updateAllLogLevelsOfComponent for pod name
	if packageName == "default" {
		p.updateAllLogLevelsOfComponent(p.podNameConfig, logLevel)
	}

}

func (p *PodLogController) propagateLogConfigForClear() {
	//For componentClass delete all the keys (except default loglevel of componentClass) ComponentClass, componentName and podName using Delete method

	//For componentName delete all the keys of  componentName and podName using Delete method

	//For podName delete all the keys of  podName using Delete method
}

//updateAllLogLevelsOfComponent propagates default loglevel to all the packages  of component
func (p *PodLogController) updateAllLogLevelsOfComponent(componentconfig *ComponentConfig, loglevel string) {
	//update the loglevel for all subkeys using Save

	for index, _ := range componentconfig.logLevel {
		componentconfig.logLevel[index].Level = loglevel
	}

	err := componentconfig.Save(componentconfig.logLevel)
	if err != nil {
		log.Error(err)
	}

}

func (p *PodLogController) loadAndApplyLogConfig() {
	//load the current configuration for pod name using Retreive method
	//create hash of loaded configuration using GenerateLogConfigHash
	//if there is previous hash stored, compare the hash to stored hash
	//if there is any change will call UpdateLogLevels
	poddata, err := p.podNameConfig.Retreive()
	if err != nil {
		log.Error(err)
	}
	podloglevel := populateLogLevel(poddata)

	//generate hash
	currentLogHash := GenerateLogConfigHash(podloglevel)

	//will set the activeHash to true and update the logHash
	if p.logHash != currentLogHash {
		p.UpdateLogLevels(podloglevel)
		p.hashActive = true
		p.logHash = currentLogHash
	}

}

func (p *PodLogController) UpdateLogLevels(logLevel []LogLevel) {
	//it should retrieve active confguration from logger
	//it should compare with new entries one by one and apply if any changes

	//currentLogLevels,_ := p.saveDefaultLogLevel()
	for _, level := range logLevel {
		if level.PackageName == "default" {
			log.SetDefaultLogLevel(log.StringToInt(level.Level))
		} else {
			pname := strings.ReplaceAll(level.PackageName, "#", "/")
			log.SetPackageLogLevel(pname, log.StringToInt(level.Level))
		}
	}
}

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
