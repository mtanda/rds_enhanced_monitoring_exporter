# RDS Enhanced Monitoring Exporter for Prometheus

Export RDS Enhanced Monitoring Metrics.

## Usage

### Setup

Allow following API call for this exporter.

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "logs:DescribeLogStreams",
                "logs:GetLogEvents"
            ],
            "Resource": [
                "arn:aws:logs:*:*:RDSOSMetrics",
                "arn:aws:logs:*:*:log-group:RDSOSMetrics:log-stream:*"
            ]
        },
        {
            "Effect": "Allow",
            "Action": [
                "rds:DescribeDBInstances",
                "rds:DescribeDBClusters",
                "tag:getResources"
            ],
            "Resource": [
                "*"
            ]
        }
    ]
}
```

### How to run

```bash
./rds_enhanced_monitoring_exporter [flags]
```

You can then query metrics from the exporter using a request like the following:

```sh
curl 'http://localhost:9408/metrics?ResourceId=db-ABCDEFGHIJKLMNOPQRSTUVWXYZ&labels[]=AvailabilityZone&labels[]=DBClusterIdentifier&labels[]=DBInstanceClass&labels[]=DBInstanceIdentifier&labels[]=Engine&labels[]=IsClusterWriter&labels[]=RDSInstanceType&labels[]=tag_Role&labels[]=tag_Cluster&labels[]=tag_Environment'
```

## Building

```sh
make
```

## Testing

```sh
go test ./...
```
