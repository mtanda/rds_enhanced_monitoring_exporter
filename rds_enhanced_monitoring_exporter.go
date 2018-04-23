package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/rds/rdsiface"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi/resourcegroupstaggingapiiface"
	"github.com/prometheus/common/log"
	"golang.org/x/sync/errgroup"
)

const (
	namespace = "rds_enhanced_monitoring"
)

type Exporter struct {
	cwLogsClient cloudwatchlogsiface.CloudWatchLogsAPI
	rdsClient    rdsiface.RDSAPI
	rgtClient    resourcegroupstaggingapiiface.ResourceGroupsTaggingAPIAPI
	lock         sync.RWMutex
	instanceMap  map[string]*rds.DBInstance
	memberMap    map[string]*rds.DBClusterMember
	tagMap       map[string]map[string]string
}

func NewExporter(region string) (*Exporter, error) {
	cfg := &aws.Config{Region: aws.String(region)}
	sess := session.Must(session.NewSession(cfg))
	return &Exporter{
		cwLogsClient: cloudwatchlogs.New(sess),
		rdsClient:    rds.New(sess),
		rgtClient:    resourcegroupstaggingapi.New(sess),
		instanceMap:  make(map[string]*rds.DBInstance),
		memberMap:    make(map[string]*rds.DBClusterMember),
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

	var dbClusters rds.DescribeDBClustersOutput
	params := &rds.DescribeDBClustersInput{}
	for {
		resp, err := e.rdsClient.DescribeDBClusters(params)
		if err != nil {
			return err
		}
		dbClusters.DBClusters = append(dbClusters.DBClusters, resp.DBClusters...)
		if resp.Marker == nil {
			break
		}
		params.Marker = resp.Marker
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
		e.instanceMap[*instance.DbiResourceId] = instance
	}
	for _, cluster := range dbClusters.DBClusters {
		for _, member := range cluster.DBClusterMembers {
			e.memberMap[*member.DBInstanceIdentifier] = member
		}
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

func outputMetrics(buf []string, m interface{}, format string, prefix string, label Labels) []string {
	mv := reflect.ValueOf(m)
	if mv.Kind() != reflect.Struct {
		return buf
	}
	for i := 0; i < mv.NumField(); i++ {
		field := mv.Field(i)
		switch field.Kind() {
		case reflect.Float64:
			buf = append(buf, fmt.Sprintf(format, prefix+mv.Type().Field(i).Name, label, field.Interface()))
		case reflect.String:
			// ignore
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
					copiedLabel["Device"] = slice.FieldByName("Device").String()
				case "FileSys":
					copiedLabel["MountPoint"] = slice.FieldByName("MountPoint").String()
					copiedLabel["Name"] = slice.FieldByName("Name").String()
				case "Network":
					copiedLabel["Device"] = slice.FieldByName("Device").String()
				}
				buf = append(buf, outputMetrics(buf, slice.Interface(), format, prefix+sliceType+"_", copiedLabel)...)
			}
		default:
			buf = append(buf, outputMetrics(buf, field.Interface(), format, prefix+field.Type().Name()+"_", label)...)
		}
	}
	return buf
}

func (e *Exporter) exportHandler(w http.ResponseWriter, r *http.Request) {
	targetLabels := r.URL.Query()["labels[]"]

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
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == "ResourceNotFoundException" {
				return
			}
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	buf := make([]string, 0)
	var mu sync.RWMutex
	eg := errgroup.Group{}
	for _, stream := range resp.LogStreams {
		s := stream
		eg.Go(func() error {
			e.lock.RLock()
			instance, ok := e.instanceMap[*s.LogStreamName]
			if !ok {
				e.lock.RUnlock()
				return nil
			}
			e.lock.RUnlock()

			events, err := e.cwLogsClient.GetLogEvents(&cloudwatchlogs.GetLogEventsInput{
				LogGroupName:  aws.String("RDSOSMetrics"),
				LogStreamName: s.LogStreamName,
				StartFromHead: aws.Bool(false),
				Limit:         aws.Int64(1),
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return err
			}

			if len(events.Events) == 0 {
				return nil
			}

			var m RDSOSMetrics
			err = json.Unmarshal([]byte(*events.Events[0].Message), &m)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return err
			}

			timestamp := *events.Events[0].Timestamp / 1000
			format := namespace + "_%s{%s} %f " + strconv.FormatInt(timestamp, 10) + "000"

			label := Labels{}
			targetTags := make(map[string]bool)
			for _, l := range targetLabels {
				switch l {
				case "DBInstanceIdentifier":
					label["DBInstanceIdentifier"] = *instance.DBInstanceIdentifier
				case "DBInstanceClass":
					label["DBInstanceClass"] = *instance.DBInstanceClass
				case "StorageType":
					label["StorageType"] = *instance.StorageType
				case "AvailabilityZone":
					label["AvailabilityZone"] = *instance.AvailabilityZone
				case "DBSubnetGroup.VpcId":
					label["VpcId"] = *instance.DBSubnetGroup.VpcId
				case "Engine":
					label["Engine"] = *instance.Engine
				case "EngineVersion":
					label["EngineVersion"] = *instance.EngineVersion
				case "IsClusterWriter":
					e.lock.RLock()
					if member, ok := e.memberMap[*instance.DBInstanceIdentifier]; ok {
						if *member.IsClusterWriter {
							label["IsClusterWriter"] = "true"
						} else {
							label["IsClusterWriter"] = "false"
						}
					}
					e.lock.RUnlock()
				case "RDSInstanceType":
					e.lock.RLock()
					switch *instance.Engine {
					case "aurora":
						if member, ok := e.memberMap[*instance.DBInstanceIdentifier]; ok {
							if *member.IsClusterWriter {
								label["RDSInstanceType"] = "writer"
							} else {
								label["RDSInstanceType"] = "reader"
							}
						}
					case "mysql":
						if instance.ReadReplicaSourceDBInstanceIdentifier == nil {
							label["RDSInstanceType"] = "master"
						} else {
							label["RDSInstanceType"] = "slave"

						}
					}
					e.lock.RUnlock()
				default:
					if strings.Index(l, "tag_") == 0 {
						targetTags[l[4:]] = true
					}
				}
			}
			e.lock.RLock()
			for k, v := range e.tagMap[*instance.DBInstanceIdentifier] {
				if targetTags[k] {
					label["tag_"+k] = v
				}
			}
			e.lock.RUnlock()
			mu.Lock()
			buf = append(buf, outputMetrics(make([]string, 0), m, format, "", label)...)
			mu.Unlock()
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		log.Errorln("error: ", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("error: %s", err)))
		return
	}
	fmt.Fprintf(w, strings.Join(buf, "\n"))
}

func GetDefaultRegion() (string, error) {
	var region string

	sess, err := session.NewSession()
	if err != nil {
		return "", err
	}
	metadata := ec2metadata.New(sess, &aws.Config{
		MaxRetries: aws.Int(0),
	})
	if metadata.Available() {
		var err error
		region, err = metadata.Region()
		if err != nil {
			return "", err
		}
	} else {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
	}

	return region, nil
}

type config struct {
	listenAddress string
	metricsPath   string
	configFile    string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listenAddress, "web.listen-address", ":9408", "Address to listen on for web endpoints.")
	flag.StringVar(&cfg.metricsPath, "web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	flag.StringVar(&cfg.configFile, "config.file", "./rds_enhanced_monitoring_exporter.yml", "Configuration file path.")
	flag.Parse()

	exporterCfg, err := LoadConfig(cfg.configFile)
	if err != nil {
		log.Fatal(err)
	}

	// set default region
	region, err := GetDefaultRegion()
	if err != nil {
		log.Fatal(err)
	}
	if len(exporterCfg.Targets) == 0 {
		exporterCfg.Targets = make([]Target, 1)
		exporterCfg.Targets[0] = Target{Region: region}
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
