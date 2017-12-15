package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/rds/rdsiface"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi/resourcegroupstaggingapiiface"
)

type mockedCloudWatchLogs struct {
	cloudwatchlogsiface.CloudWatchLogsAPI
}

func (c mockedCloudWatchLogs) DescribeLogStreamsPages(input *cloudwatchlogs.DescribeLogStreamsInput, fn func(*cloudwatchlogs.DescribeLogStreamsOutput, bool) bool) error {
	fn(&cloudwatchlogs.DescribeLogStreamsOutput{
		LogStreams: []*cloudwatchlogs.LogStream{
			&cloudwatchlogs.LogStream{
				LogStreamName: aws.String("db-AAAAAAAAAAAAAAAAAAAAAAAAAA"),
			},
			&cloudwatchlogs.LogStream{
				LogStreamName: aws.String("db-BBBBBBBBBBBBBBBBBBBBBBBBBB"),
			},
		},
	}, false)
	return nil
}

func (c mockedCloudWatchLogs) GetLogEvents(input *cloudwatchlogs.GetLogEventsInput) (*cloudwatchlogs.GetLogEventsOutput, error) {
	genMessage := func(instanceID string, instanceResourceID string) string {
		return strings.Replace(strings.Replace(`
{
	"engine": "MYSQL",
	"instanceID": "__instanceID__",
	"instanceResourceID": "__instanceResourceID__",
	"timestamp": "2017-12-01T00:00:00Z",
	"version": 1,
	"uptime": "1 days, 00:00:00",
	"numVCPUs": 2,
	"cpuUtilization": {
		"guest": 0,
		"irq": 0,
		"system": 0.47,
		"wait": 0.13,
		"idle": 98.13,
		"user": 1,
		"total": 1.87,
		"steal": 0.07,
		"nice": 0.2
	},
	"loadAverageMinute": {
		"fifteen": 0,
		"five": 0,
		"one": 0
	},
	"memory": {
		"writeback": 0,
		"hugePagesFree": 0,
		"hugePagesRsvd": 0,
		"hugePagesSurp": 0,
		"cached": 1240780,
		"hugePagesSize": 2048,
		"free": 95292,
		"hugePagesTotal": 0,
		"inactive": 636288,
		"pageTables": 8048,
		"dirty": 208,
		"mapped": 38508,
		"active": 1175960,
		"total": 2051520,
		"slab": 80736,
		"buffers": 206620
	},
	"tasks": {
		"sleeping": 186,
		"zombie": 0,
		"running": 3,
		"stopped": 0,
		"total": 189,
		"blocked": 0
	},
	"swap": {
		"cached": 0,
		"total": 4095996,
		"out": 0,
		"free": 4095996,
		"in": 0
	},
	"network": [
		{
			"interface": "eth0",
			"rx": 1323.67,
			"tx": 5342
		}
	],
	"diskIO": [
		{
			"writeKbPS": 2.13,
			"readIOsPS": 0,
			"await": 0,
			"readKbPS": 0,
			"rrqmPS": 0,
			"util": 0,
			"avgQueueLen": 0,
			"tps": 0.53,
			"readKb": 0,
			"device": "rdsdev",
			"writeKb": 32,
			"avgReqSz": 4,
			"wrqmPS": 0,
			"writeIOsPS": 0.53
		}
	],
	"fileSys": [
		{
			"used": 4748148,
			"name": "rdsfilesys",
			"usedFiles": 1471,
			"usedFilePercent": 0.11,
			"maxFiles": 1310720,
			"mountPoint": "/rdsdbdata",
			"total": 20496384,
			"usedPercent": 23.17
		}
	]
}`, "__instanceID__", instanceID, 1), "__instanceResourceID__", instanceResourceID, 1)
	}
	return &cloudwatchlogs.GetLogEventsOutput{
		Events: []*cloudwatchlogs.OutputLogEvent{
			&cloudwatchlogs.OutputLogEvent{
				Message:   aws.String(genMessage("AAA", "db-AAAAAAAAAAAAAAAAAAAAAAAAAA")),
				Timestamp: aws.Int64(1486977657000),
			},
			&cloudwatchlogs.OutputLogEvent{
				Message:   aws.String(genMessage("BBB", "db-BBBBBBBBBBBBBBBBBBBBBBBBBB")),
				Timestamp: aws.Int64(1486977657000),
			},
		},
	}, nil
}

type mockedRDS struct {
	rdsiface.RDSAPI
}

func (c mockedRDS) DescribeDBInstancesPages(input *rds.DescribeDBInstancesInput, fn func(*rds.DescribeDBInstancesOutput, bool) bool) error {
	fn(&rds.DescribeDBInstancesOutput{
		DBInstances: []*rds.DBInstance{
			&rds.DBInstance{
				DbiResourceId:        aws.String("db-AAAAAAAAAAAAAAAAAAAAAAAAAA"),
				DBInstanceIdentifier: aws.String("AAA"),
				DBInstanceClass:      aws.String("db.t2.meduim"),
				StorageType:          aws.String("gp2"),
				AvailabilityZone:     aws.String("us-east-1a"),
				DBSubnetGroup: &rds.DBSubnetGroup{
					VpcId: aws.String("vpc-aaaaaaaa"),
				},
				Engine:        aws.String("mysql"),
				EngineVersion: aws.String("5.7"),
			},
			&rds.DBInstance{
				DbiResourceId:        aws.String("db-BBBBBBBBBBBBBBBBBBBBBBBBBB"),
				DBInstanceIdentifier: aws.String("BBB"),
				DBInstanceClass:      aws.String("db.t2.meduim"),
				StorageType:          aws.String("gp2"),
				AvailabilityZone:     aws.String("us-east-1a"),
				DBSubnetGroup: &rds.DBSubnetGroup{
					VpcId: aws.String("vpc-aaaaaaaa"),
				},
				Engine:        aws.String("mysql"),
				EngineVersion: aws.String("5.7"),
			},
		},
	}, false)
	return nil
}

func (c mockedRDS) DescribeDBClusters(input *rds.DescribeDBClustersInput) (*rds.DescribeDBClustersOutput, error) {
	return &rds.DescribeDBClustersOutput{
		DBClusters: []*rds.DBCluster{
			&rds.DBCluster{
				DBClusterMembers: []*rds.DBClusterMember{
					&rds.DBClusterMember{
						DBInstanceIdentifier: aws.String("AAA"),
						IsClusterWriter:      aws.Bool(true),
					},
					&rds.DBClusterMember{
						DBInstanceIdentifier: aws.String("BBB"),
						IsClusterWriter:      aws.Bool(false),
					},
				},
			},
		},
	}, nil
}

type mockedRGT struct {
	resourcegroupstaggingapiiface.ResourceGroupsTaggingAPIAPI
}

func (c mockedRGT) GetResourcesPages(input *resourcegroupstaggingapi.GetResourcesInput, fn func(*resourcegroupstaggingapi.GetResourcesOutput, bool) bool) error {
	fn(&resourcegroupstaggingapi.GetResourcesOutput{
		ResourceTagMappingList: []*resourcegroupstaggingapi.ResourceTagMapping{
			&resourcegroupstaggingapi.ResourceTagMapping{
				ResourceARN: aws.String("arn:aws:rds:us-east-1:111111111111:db:AAA"),
				Tags: []*resourcegroupstaggingapi.Tag{
					&resourcegroupstaggingapi.Tag{
						Key:   aws.String("Environment"),
						Value: aws.String("production"),
					},
				},
			},
			&resourcegroupstaggingapi.ResourceTagMapping{
				ResourceARN: aws.String("arn:aws:rds:us-east-1:111111111111:db:BBB"),
				Tags: []*resourcegroupstaggingapi.Tag{
					&resourcegroupstaggingapi.Tag{
						Key:   aws.String("Environment"),
						Value: aws.String("production"),
					},
				},
			},
		},
	}, false)
	return nil
}

func TestE2E(t *testing.T) {
	e := &Exporter{
		cwLogsClient: mockedCloudWatchLogs{},
		rdsClient:    mockedRDS{},
		rgtClient:    mockedRGT{},
		instanceMap:  make(map[string]*rds.DBInstance),
		memberMap:    make(map[string]*rds.DBClusterMember),
		tagMap:       make(map[string]map[string]string),
	}
	e.collectRdsInfo()
	writer := httptest.NewRecorder()
	request := &http.Request{
		URL: &url.URL{
			RawQuery: "labels[]=DBInstanceIdentifier&labels[]=DBInstanceClass&labels[]=StorageType&labels[]=AvailabilityZone&labels[]=DBSubnetGroup.VpcId&labels[]=Engine&labels[]=EngineVersion&labels[]=IsClusterWriter&labels[]=tag_Environment",
		},
	}
	e.exportHandler(writer, request)

	body, err := ioutil.ReadAll(writer.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(string(body), "\n")[0]
	expect := "rds_enhanced_monitoring_CpuUtilization_Guest{AvailabilityZone=\"us-east-1a\",DBInstanceClass=\"db.t2.meduim\",DBInstanceIdentifier=\"AAA\",Engine=\"mysql\",EngineVersion=\"5.7\",IsClusterWriter=\"true\",StorageType=\"gp2\",VpcId=\"vpc-aaaaaaaa\",tag_Environment=\"production\"} 0.000000 1486977657"
	if expect != got {
		t.Errorf("expected %f, got %f", expect, got)
	}
}
