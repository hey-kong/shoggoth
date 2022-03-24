/*
Copyright 2021 The KubeEdge Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dataset

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"metaedge/cmd/metaedge-edge/app/options"
	metaedgev1 "metaedge/pkg/apis/metaedge/v1alpha1"
	"metaedge/pkg/cloud/runtime"
	"metaedge/pkg/edge/db/sqlite"
	"metaedge/pkg/edge/handler"
	"metaedge/pkg/edge/storage"
	workertypes "metaedge/pkg/edge/worker"
	"metaedge/pkg/messagelayer"
	"metaedge/pkg/util"
)

const (
	// MonitorDataSourceIntervalSeconds is interval time of monitoring data source
	MonitorDataSourceIntervalSeconds = 60
	// KindName is kind of dataset resource
	KindName = "dataset"
	// CSVFormat is commas separated value format with an extra header.
	// It can be used in structured data scenarios.
	CSVFormat = "csv"
	// TXTFormat is line separated format.
	// It can be used in unstructured data scenarios.
	TXTFormat = "txt"
)

// Dataset defines config for dataset
type Dataset struct {
	*metaedgev1.Dataset
	DataSource *DataSource `json:"dataSource"`
	Done       chan struct{}
	URLPrefix  string
	Storage    storage.Storage
}

// DatasetManager defines dataset manager
type DatasetManager struct {
	Client            handler.ClientI
	DatasetMap        map[string]*Dataset
	VolumeMountPrefix string
}

// DataSource defines config for data source
type DataSource struct {
	TrainSamples    []string
	NumberOfSamples int
	Header          string
}

// New creates a dataset manager
func New(client handler.ClientI, options *options.LocalControllerOptions) *DatasetManager {
	dm := DatasetManager{
		Client:            client,
		DatasetMap:        make(map[string]*Dataset),
		VolumeMountPrefix: options.VolumeMountPrefix,
	}

	return &dm
}

// Start starts dataset manager
func (dm *DatasetManager) Start() error {
	return nil
}

// GetDataset gets dataset
func (dm *DatasetManager) GetDataset(name string) (*Dataset, bool) {
	d, ok := dm.DatasetMap[name]
	return d, ok
}

// Insert inserts dataset to db
func (dm *DatasetManager) Insert(message *messagelayer.Message) error {
	name := util.GetUniqueIdentifier(message.Namespace, message.ResourceName, message.ResourceKind)
	first := false
	ds, ok := dm.GetDataset(name)
	if !ok {
		ds = &Dataset{}
		ds.Storage = storage.Storage{IsLocalStorage: false}
		ds.Done = make(chan struct{})
		dm.DatasetMap[name] = ds
		first = true
	}

	if err := json.Unmarshal(message.Content, ds); err != nil {
		return err
	}

	credential := ds.ObjectMeta.Annotations[runtime.SecretAnnotationKey]
	if credential != "" {
		if err := ds.Storage.SetCredential(credential); err != nil {
			return fmt.Errorf("failed to set dataset(name=%s)'s storage credential, error: %+v", name, err)
		}
	}

	isLocalURL, err := ds.Storage.IsLocalURL(ds.Spec.URL)
	if err != nil {
		return fmt.Errorf("dataset(name=%s)'s url is invalid, error: %+v", name, err)
	}
	if isLocalURL {
		ds.Storage.IsLocalStorage = true
	}

	if first {
		go dm.monitorDataSources(name)
	}

	if err := sqlite.SaveResource(name, ds.TypeMeta, ds.ObjectMeta, ds.Spec); err != nil {
		return err
	}

	return nil
}

// Delete deletes dataset config in db
func (dm *DatasetManager) Delete(message *messagelayer.Message) error {
	name := util.GetUniqueIdentifier(message.Namespace, message.ResourceName, message.ResourceKind)

	if ds, ok := dm.GetDataset(name); ok && ds.Done != nil {
		close(ds.Done)
	}

	delete(dm.DatasetMap, name)

	if err := sqlite.DeleteResource(name); err != nil {
		return err
	}

	return nil
}

// monitorDataSources monitors the data url of specified dataset
func (dm *DatasetManager) monitorDataSources(name string) {
	ds, ok := dm.GetDataset(name)
	if !ok || ds == nil {
		return
	}

	dataURL := ds.Spec.URL
	if ds.Storage.IsLocalStorage {
		dataURL = util.AddPrefixPath(dm.VolumeMountPrefix, dataURL)
	}

	ds.URLPrefix = strings.TrimRight(dataURL, filepath.Base(dataURL))
	samplesNumber := 0
	for {
		select {
		case <-ds.Done:
			return
		default:
		}

		dataSource, err := ds.getDataSource(dataURL, ds.Spec.Format)
		if err != nil {
			klog.Errorf("dataset(name=%s) get samples from %s failed, error: %+v", name, dataURL, err)
		} else {
			ds.DataSource = dataSource
			if samplesNumber != dataSource.NumberOfSamples {
				samplesNumber = dataSource.NumberOfSamples
				klog.Infof("dataset(name=%s) get samples from data source(url=%s) successfully. number of samples: %d",
					name, dataURL, dataSource.NumberOfSamples)

				header := messagelayer.MessageHeader{
					Namespace:    ds.Namespace,
					ResourceKind: ds.Kind,
					ResourceName: ds.Name,
					Operation:    handler.StatusOperation,
				}

				if err := dm.Client.WriteMessage(struct {
					NumberOfSamples int `json:"numberOfSamples"`
				}{
					dataSource.NumberOfSamples,
				}, header); err != nil {
					klog.Errorf("dataset(name=%s) publish samples info failed, error: %+v", name, err)
				}
			}
		}
		<-time.After(MonitorDataSourceIntervalSeconds * time.Second)
	}
}

// getDataSource gets data source info
func (ds *Dataset) getDataSource(dataURL string, format string) (*DataSource, error) {
	if err := ds.validFormat(format); err != nil {
		return nil, err
	}

	localURL, err := ds.Storage.Download(dataURL, "")

	if !ds.Storage.IsLocalStorage {
		defer os.RemoveAll(localURL)
	}

	if err != nil {
		return nil, err
	}

	return ds.readByLine(localURL, format)
}

// readByLine reads file by line
func (ds *Dataset) readByLine(url string, format string) (*DataSource, error) {
	samples, err := GetSamples(url)
	if err != nil {
		klog.Errorf("read file %s failed, error: %v", url, err)
		return nil, err
	}

	numberOfSamples := 0
	dataSource := DataSource{}
	switch strings.ToLower(format) {
	case TXTFormat:
		numberOfSamples += len(samples)
	case CSVFormat:
		// the first row of csv file is header
		if len(samples) == 0 {
			return nil, fmt.Errorf("file %s is empty", url)
		}
		dataSource.Header = samples[0]
		samples = samples[1:]
		numberOfSamples += len(samples)

	default:
		return nil, fmt.Errorf("invaild file format")
	}

	dataSource.TrainSamples = samples
	dataSource.NumberOfSamples = numberOfSamples

	return &dataSource, nil
}

func (dm *DatasetManager) GetName() string {
	return KindName
}

func (dm *DatasetManager) AddWorkerMessage(message workertypes.MessageContent) {
	// dummy
}

// GetSamples gets samples in a file
func GetSamples(url string) ([]string, error) {
	var samples []string
	if !util.IsExists(url) {
		return nil, fmt.Errorf("url(%s) does not exist", url)
	}

	if !util.IsFile(url) {
		return nil, fmt.Errorf("url(%s) is not a file, not vaild", url)
	}

	file, err := os.Open(url)
	if err != nil {
		klog.Errorf("read %s failed, error: %v", url, err)
		return samples, err
	}

	fileScanner := bufio.NewScanner(file)
	for fileScanner.Scan() {
		samples = append(samples, fileScanner.Text())
	}

	if err = file.Close(); err != nil {
		klog.Errorf("close file(url=%s) failed, error: %v", url, err)
		return samples, err
	}

	return samples, nil
}

// validFormat checks data format is valid
func (ds *Dataset) validFormat(format string) error {
	for _, v := range []string{TXTFormat, CSVFormat} {
		if strings.ToLower(format) == v {
			return nil
		}
	}

	return fmt.Errorf("dataset format(%s) is invalid", format)
}
