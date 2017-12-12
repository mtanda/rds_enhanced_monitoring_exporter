# RDS Enhanced Monitorijng Exporter for Prometheus

Export RDS Enhanced Monitoring Metrics.

## Getting Started

To run it:

```bash
./rds_enhanced_monitoring_exporter [flags]
```

Help on flags:

```bash
./rds_enhanced_monitoring_exporter --help
```

## Usage

### AWS IAM

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
                "tag:getResources"
            ],
            "Resource": [
                "*"
            ]
        }
    ]
}
```

### Building

```bash
make
```

## License

Apache License 2.0, see [LICENSE](https://github.com/mtanda/rds_enhanced_monitoring_exporter/blob/master/LICENSE).
