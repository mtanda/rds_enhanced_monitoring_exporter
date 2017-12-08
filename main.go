package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/prometheus/common/log"
)

const (
	namespace = "rds_enhanced_monitoring"
)

type Exporter struct {
	cwLogsClient *cloudwatchlogs.CloudWatchLogs
	rdsClient    *rds.RDS
	rgtClient    *resourcegroupstaggingapi.ResourceGroupsTaggingAPI
	lock         sync.RWMutex
	instanceMap  map[string]rds.DBInstance
	tagMap       map[string]map[string]string
}

func NewExporter(region string) (*Exporter, error) {
	cfg := &aws.Config{Region: aws.String(region)}
	sess := session.Must(session.NewSession(cfg))
	return &Exporter{
		cwLogsClient: cloudwatchlogs.New(sess),
		rdsClient:    rds.New(sess),
		rgtClient:    resourcegroupstaggingapi.New(sess),
		instanceMap:  make(map[string]rds.DBInstance),
		tagMap:       make(map[string]map[string]string),
	}, nil
}

func (e *Exporter) collectRdsInfo() error {
	var dbInstances rds.DescribeDBInstancesOutput
	err := e.rdsClient.DescribeDBInstancesPages(&rds.DescribeDBInstancesInput{},
		func(page *rds.DescribeDBInstancesOutput, lastPage bool) bool {
			instances, _ := awsutil.ValuesAtPath(page, "DBInstances")
			for _, instance := range instances {
				dbInstances.DBInstances = append(dbInstances.DBInstances, instance.(*rds.DBInstance))
			}
			return !lastPage
		})
	if err != nil {
		return err
	}

	var resources resourcegroupstaggingapi.GetResourcesOutput
	err = e.rgtClient.GetResourcesPages(&resourcegroupstaggingapi.GetResourcesInput{
		ResourceTypeFilters: []*string{aws.String("rds:db")},
		TagsPerPage:         aws.Int64(500),
	}, func(page *resourcegroupstaggingapi.GetResourcesOutput, lastPage bool) bool {
		mappings, _ := awsutil.ValuesAtPath(page, "ResourceTagMappingList")
		for _, mapping := range mappings {
			resources.ResourceTagMappingList = append(resources.ResourceTagMappingList, mapping.(*resourcegroupstaggingapi.ResourceTagMapping))
		}
		//return !lastPage // not work
		pagenationToken, _ := awsutil.ValuesAtPath(page, "PagenationToken")
		return pagenationToken != nil
	})
	if err != nil {
		return err
	}

	e.lock.Lock()
	for _, instance := range dbInstances.DBInstances {
		e.instanceMap[*instance.DbiResourceId] = *instance
	}
	for _, mapping := range resources.ResourceTagMappingList {
		instanceID := strings.Split(*mapping.ResourceARN, ":")[6]
		e.tagMap[instanceID] = make(map[string]string)
		for _, tag := range mapping.Tags {
			e.tagMap[instanceID][*tag.Key] = *tag.Value
		}
	}
	e.lock.Unlock()

	return nil
}

func outputMetrics(w http.ResponseWriter, m interface{}, format string, prefix string, label Labels) {
	mv := reflect.ValueOf(m)
	if mv.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < mv.NumField(); i++ {
		field := mv.Field(i)
		switch field.Kind() {
		case reflect.Float64:
			fmt.Fprintf(w, format, prefix+mv.Type().Field(i).Name, label, field.Interface())
			break
		case reflect.String:
			// ignore
			break
		case reflect.Slice:
			for i := 0; i < field.Len(); i++ {
				copiedLabel := make(Labels)
				for k, v := range label {
					copiedLabel[k] = v
				}

				slice := field.Index(i)
				sliceType := slice.Type().Name()
				switch sliceType {
				case "DiskIO":
					copiedLabel["__Device__"] = slice.FieldByName("Device").String()
					break
				case "FileSys":
					copiedLabel["__MountPoint__"] = slice.FieldByName("MountPoint").String()
					copiedLabel["__Name__"] = slice.FieldByName("Name").String()
					break
				case "Network":
					copiedLabel["__Device__"] = slice.FieldByName("Device").String()
					break
				}
				outputMetrics(w, slice.Interface(), format, prefix+sliceType+"_", copiedLabel)
			}
			break
		default:
			outputMetrics(w, field.Interface(), format, prefix+field.Type().Name()+"_", label)
			break
		}
	}
}

func (e *Exporter) exportHandler(w http.ResponseWriter, r *http.Request) {
	var resp cloudwatchlogs.DescribeLogStreamsOutput
	err := e.cwLogsClient.DescribeLogStreamsPages(&cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: aws.String("RDSOSMetrics"),
		OrderBy:      aws.String("LastEventTime"),
		Descending:   aws.Bool(true),
	}, func(page *cloudwatchlogs.DescribeLogStreamsOutput, lastPage bool) bool {
		streams, _ := awsutil.ValuesAtPath(page, "LogStreams")
		for _, stream := range streams {
			resp.LogStreams = append(resp.LogStreams, stream.(*cloudwatchlogs.LogStream))
		}
		return !lastPage
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, stream := range resp.LogStreams {
		e.lock.RLock()
		instance, ok := e.instanceMap[*stream.LogStreamName]
		if !ok {
			e.lock.RUnlock()
			continue
		}
		e.lock.RUnlock()

		events, err := e.cwLogsClient.GetLogEvents(&cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  aws.String("RDSOSMetrics"),
			LogStreamName: stream.LogStreamName,
			StartFromHead: aws.Bool(false),
			Limit:         aws.Int64(1),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if len(events.Events) == 0 {
			continue
		}

		var m RDSOSMetrics
		err = json.Unmarshal([]byte(*events.Events[0].Message), &m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		timestamp := *events.Events[0].Timestamp / 1000
		format := namespace + "_%s{%s} %f " + strconv.FormatInt(timestamp, 10) + "\n"
		label := Labels{
			"__InstanceID__":       *instance.DBInstanceIdentifier,
			"__DBInstanceClass__":  *instance.DBInstanceClass,
			"__StorageType__":      *instance.StorageType,
			"__AvailabilityZone__": *instance.AvailabilityZone,
			"__VpcId__":            *instance.DBSubnetGroup.VpcId,
			"__Engine__":           *instance.Engine,
			"__EngineVersion__":    *instance.EngineVersion,
		}
		e.lock.RLock()
		for k, v := range e.tagMap[*instance.DBInstanceIdentifier] {
			label["__tag_"+k+"__"] = v
		}
		e.lock.RUnlock()
		outputMetrics(w, m, format, "", label)
	}
}

type config struct {
	listenAddress string
	metricsPath   string
	configFile    string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listenAddress, "web.listen-address", ":9201", "Address to listen on for web endpoints.")
	flag.StringVar(&cfg.metricsPath, "web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	flag.StringVar(&cfg.configFile, "config.file", "./rds_enhanced_monitoring.yml", "Configuration file path.")
	flag.Parse()

	exporterCfg, err := LoadConfig(cfg.configFile)
	if err != nil {
		log.Fatal(err)
	}

	exporter, err := NewExporter(exporterCfg.Targets[0].Region)
	if err != nil {
		log.Fatal(err)
	}

	exporter.collectRdsInfo()
	go func() {
		t := time.NewTimer(0)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				t.Reset(5 * time.Minute)
				err := exporter.collectRdsInfo()
				if err != nil {
					log.Warn(err)
				}
			}
		}
	}()

	http.HandleFunc(cfg.metricsPath, exporter.exportHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>RDS Enhanced Monitoring Exporter</title></head>
			<body>
			<h1>RDS Enhanced Monitoring Exporter</h1>
			<p><a href="` + cfg.metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	log.Infoln("Listening on", cfg.listenAddress)
	err = http.ListenAndServe(cfg.listenAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}
