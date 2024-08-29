package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cloudwatchlogsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/prometheus/common/log"
	"golang.org/x/sync/errgroup"
)

const (
	namespace = "rds_enhanced_monitoring"
)

type Exporter struct {
	cwLogsClient *cloudwatchlogs.Client
	rdsClient    *rds.Client
	rgtClient    *resourcegroupstaggingapi.Client
	lock         sync.RWMutex
	instanceMap  map[string]rdsTypes.DBInstance
	memberMap    map[string]rdsTypes.DBClusterMember
	tagMap       map[string]map[string]string
	lastUpdated  map[string]map[string]int64
}

func NewExporter(ctx context.Context, region string) (*Exporter, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &Exporter{
		cwLogsClient: cloudwatchlogs.NewFromConfig(awsCfg),
		rdsClient:    rds.NewFromConfig(awsCfg),
		rgtClient:    resourcegroupstaggingapi.NewFromConfig(awsCfg),
		instanceMap:  make(map[string]rdsTypes.DBInstance),
		memberMap:    make(map[string]rdsTypes.DBClusterMember),
		tagMap:       make(map[string]map[string]string),
		lastUpdated:  make(map[string]map[string]int64),
	}, nil
}

func (e *Exporter) collectRdsInfo(ctx context.Context) error {
	var dbInstances rds.DescribeDBInstancesOutput
	rdsPaginator := rds.NewDescribeDBInstancesPaginator(e.rdsClient, &rds.DescribeDBInstancesInput{})
	for rdsPaginator.HasMorePages() {
		output, err := rdsPaginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, instance := range output.DBInstances {
			dbInstances.DBInstances = append(dbInstances.DBInstances, instance)
		}
	}

	var dbClusters rds.DescribeDBClustersOutput
	params := &rds.DescribeDBClustersInput{}
	for {
		resp, err := e.rdsClient.DescribeDBClusters(ctx, params)
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
	rgtPaginator := resourcegroupstaggingapi.NewGetResourcesPaginator(
		e.rgtClient,
		&resourcegroupstaggingapi.GetResourcesInput{
			ResourceTypeFilters: []string{"rds:db"},
			TagsPerPage:         aws.Int32(500),
		},
	)
	for rgtPaginator.HasMorePages() {
		output, err := rgtPaginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, mapping := range output.ResourceTagMappingList {
			resources.ResourceTagMappingList = append(resources.ResourceTagMappingList, mapping)
		}
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
				case "PhysicalDeviceIO":
					copiedLabel["Device"] = slice.FieldByName("Device").String()
				case "FileSys":
					copiedLabel["MountPoint"] = slice.FieldByName("MountPoint").String()
					copiedLabel["Name"] = slice.FieldByName("Name").String()
				case "Network":
					copiedLabel["Device"] = slice.FieldByName("Device").String()
				}
				buf = append(buf, outputMetrics(make([]string, 0), slice.Interface(), format, prefix+sliceType+"_", copiedLabel)...)
			}
		default:
			buf = append(buf, outputMetrics(make([]string, 0), field.Interface(), format, prefix+field.Type().Name()+"_", label)...)
		}
	}
	return buf
}

func (e *Exporter) exportHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	remoteAddr := strings.Split(r.RemoteAddr, ":")[0]
	if _, ok := e.lastUpdated[remoteAddr]; !ok {
		e.lastUpdated[remoteAddr] = make(map[string]int64)
	}
	targetResourceId := r.URL.Query().Get("ResourceId")

	targetLabels := r.URL.Query()["labels[]"]

	targetStreams := make([]string, 1)
	if len(targetResourceId) == 0 {
		paginator := cloudwatchlogs.NewDescribeLogStreamsPaginator(
			e.cwLogsClient,
			&cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName: aws.String("RDSOSMetrics"),
				OrderBy:      "LastEventTime",
				Descending:   aws.Bool(true),
			},
		)
		for paginator.HasMorePages() {
			output, err := paginator.NextPage(ctx)
			if err != nil {
				var rnfe *cloudwatchlogsTypes.ResourceNotFoundException
				if errors.As(err, &rnfe) {
					log.Infoln("RDSOSMetrics is not found")
					return
				}
				log.Errorln("error: calling DescribeLogStreams is failed")
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, stream := range output.LogStreams {
				if time.Unix(*stream.LastEventTimestamp/1000, 0).After(time.Now().Add(-1 * time.Hour)) {
					targetStreams = append(targetStreams, *stream.LogStreamName)
				}
			}
		}
	} else {
		targetStreams[0] = targetResourceId
	}

	buf := make([]string, 0)
	var mu sync.RWMutex
	eg := errgroup.Group{}
	ch := make(chan int, 5)
	for _, stream := range targetStreams {
		ch <- 1
		s := stream
		eg.Go(func() error {
			waitTime := time.Now().Add(1 * time.Second)
			defer func() {
				now := time.Now()
				if now.Before(waitTime) {
					time.Sleep(waitTime.Sub(now))
				}
				<-ch
			}()
			e.lock.RLock()
			instance, ok := e.instanceMap[s]
			if !ok {
				e.lock.RUnlock()
				log.Errorf("error: %s is not found in instanceMap", s)
				return nil
			}
			e.lock.RUnlock()

			input := &cloudwatchlogs.GetLogEventsInput{
				LogGroupName:  aws.String("RDSOSMetrics"),
				LogStreamName: aws.String(s),
				StartFromHead: aws.Bool(false),
				Limit:         aws.Int32(3),
			}
			if _, ok := e.lastUpdated[remoteAddr][s]; ok {
				input.StartTime = aws.Int64(e.lastUpdated[remoteAddr][s] + 1)
			}
			events, err := e.cwLogsClient.GetLogEvents(ctx, input)
			if err != nil {
				return err
			}

			if len(events.Events) == 0 {
				log.Infoln("GetLogEvents response is empty")
				return nil
			}

			var m RDSOSMetrics
			for _, event := range events.Events {
				err = json.Unmarshal([]byte(*event.Message), &m)
				if err != nil {
					return err
				}

				timestamp := *event.Timestamp / 1000
				if *event.Timestamp > e.lastUpdated[remoteAddr][s] {
					e.lastUpdated[remoteAddr][s] = *event.Timestamp
				}
				format := namespace + "_%s{%s} %f " + strconv.FormatInt(timestamp, 10) + "000"

				label := Labels{}
				targetTags := make(map[string]bool)
				for _, l := range targetLabels {
					switch l {
					case "DBInstanceIdentifier":
						label["DBInstanceIdentifier"] = *instance.DBInstanceIdentifier
					case "DBClusterIdentifier":
						switch *instance.Engine {
						case "aurora":
							fallthrough
						case "aurora-mysql":
							label["DBClusterIdentifier"] = *instance.DBClusterIdentifier
						}
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
							fallthrough
						case "aurora-mysql":
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
			}

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

var regionCache = ""

func GetDefaultRegion() (string, error) {
	var region string

	if regionCache != "" {
		return regionCache, nil
	}

	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(0))
	if err != nil {
		return "", err
	}

	client := imds.NewFromConfig(cfg)
	response, err := client.GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
	} else {
		region = response.Region
		if region != "" {
			regionCache = region
		}
	}

	return region, nil
}

type flagConfig struct {
	listenAddress string
	metricsPath   string
	configFile    string
}

func main() {
	var cfg flagConfig
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
	ctx := context.TODO()
	exporter, err := NewExporter(ctx, region)
	if err != nil {
		log.Fatal(err)
	}

	err = exporter.collectRdsInfo(ctx)
	if err != nil {
		log.Warn(err)
	}
	go func() {
		t := time.NewTimer(0)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				t.Reset(5 * time.Minute)
				err = exporter.collectRdsInfo(ctx)
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
