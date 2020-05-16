# s3sync2local

[![Go Report Card](https://goreportcard.com/badge/github.com/ttys3/s3sync2local)](https://goreportcard.com/report/github.com/ttys3/s3sync2local)

## Description

The `s3sync2local` tool is asynchronously syncing data from S3 storage
service to local filesystem for multiple _sites_ (path + bucket
combination).

On start, the `s3sync2local` launches pool of generic download workers,
checksum workers for each _site_.  
Once all of the above launched it starts comparing local directory
contents with S3 (using checksums<->ETag and also validates
StorageClass)  
which might take quite a while depending on the size of your data
directory, disk speed, and available CPU resources.  
All the new files or removed files are put into the download queue for
processing.

once sync done, the `s3sync2local` tool will exit.


## Running the s3sync2local

1. Create directory with [configuration file](#Configuration), eg. -
   `/path/to/config/config.yml`.
2. Run docker container with providing AWS credentials via environment
   variables (IAM role should also do the trick), alternatively
   credentials could be provided in the [config file](#Configuration),
   mount directory containing the config file and all of the backup
   directories listed in the config file:

```bash
docker run --rm -ti \
-e "AWS_ACCESS_KEY_ID=AKIAI44QH8DHBEXAMPLE" \
-e "AWS_SECRET_ACCESS_KEY=je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY" \
-e "AWS_DEFAULT_REGION=us-east-1" \
-v "/path/to/config:/opt/s3sync2local" \
-v "/backup/path:/backup" \
zmazay/s3sync2local \
./s3sync2local -config /opt/s3sync2local/config.yml
```

## Configuration

The `bucket_path` now support template vars.
It will pick yesterday's date so it will be very convenient to run a daily cron job.
you can set it like `images/{{.Year}}/{{.Month}}/{{.Day}}`

Example configuration:

```yaml
download_workers: 10
sites:
- local_path: /local/path1
  bucket: backup-bucket-path1
  bucket_region: us-east-1
  storage_class: STANDARD_IA
  access_key: AKIAI44QH8DHBEXAMPLE
  secret_access_key: je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY
  exclusions:
    - .[Dd][Ss]_[Ss]tore
    - .[Aa]pple[Dd]ouble
- local_path: /local/path2
  bucket: backup-bucket-path2
  bucket_path: path2
  exclusions:
    - "[Tt]humbs.db"
- local_path: /local/path3
  bucket: backup-bucket-path3
  bucket_path: path3
  exclusions:
    - "[Tt]humbs.db"
```

### Command line args

```bash
-config string
    Path to the config.yml (default "config.yml")
-metrics-path string
    Prometheus exporter path (default "/metrics")
-metrics-port string
    Prometheus exporter port, 0 to disable the exporter (default "9350")
```

### Generic configuration options

| Variable              | Description                                                                                                                                                                                                        | Default | Required |    |    |    |
|:----------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------|:---------|:---|:---|:---|
| access_key            | Global AWS Access Key                                                                                                                                                                                              | n/a     | no       |    |    |    |
| secret_access_key     | Global AWS Secret Access Key                                                                                                                                                                                       | n/a     | no       |    |    |    |
| aws_region            | AWS region                                                                                                                                                                                                         | n/a     | no       |    |    |    |
| endpoint              | AWS S3 Endpoint                                                                                                                                                                                                    |         |          |    |    |    |
| loglevel              | Logging level, valid options are - `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`. With log level set to `trace` logger will output everything, with `debug` everything apart from `trace` and so on. | `info`  | no       |    |    |    |
| download_queue_buffer | Number of elements in the download queue waiting for processing, might improve performance, however, increases memory usage                                                                                        | `0`     | no       |    |    |    |
| checksum_workers      | Number of checksum workers for the service                                                                                                                                                                         | `CPU*2` | no       |    |    |    |
| download_workers      | Number of download workers for the service                                                                                                                                                                         | `10`    | no       |    |    |    |
| watch_interval        | Interval for file system watcher in milliseconds                                                                                                                                                                   | `1000`  | no       |    |    |    |

### Site configuration options

| Variable          | Description                                                                                             | Default                    | Required |
|:------------------|:--------------------------------------------------------------------------------------------------------|:---------------------------|:---------|
| name              | Human friendly site name                                                                                | `bucket/bucket_path`       | no       |
| local_path        | Local file system path to be synced with S3, **using relative path is known to cause some issues**.     | n/a                        | yes      |
| bucket            | S3 bucket name                                                                                          | n/a                        | yes      |
| bucket_path       | S3 path prefix                                                                                          | n/a                        | no       |
| bucket_region     | S3 bucket region                                                                                        | `global.aws_region`        | no       |
| retire_deleted    | Remove files locally from S3 which do not exist from S3                                                 | `false`                    | no       |
| storage_class     | [S3 storage class](https://docs.aws.amazon.com/AmazonS3/latest/dev/storage-class-intro.html#sc-compare) | `STANDARD`                 | no       |
| access_key        | Site AWS Access Key                                                                                     | `global.access_key`        | no       |
| secret_access_key | Site AWS Secret Access Key                                                                              | `global.secret_access_key` | no       |
| watch_interval    | Interval for file system watcher in milliseconds, overrides global setting                              | `global.watch_interval`    | no       |
| exclusions        | List of regex filters for exclusions                                                                    | n/a                        | no       |

### Gotchas

1. AWS credentials and region have the following priority:
   1. Site AWS credentials (region)
   2. Global AWS credentials (region)
   3. Environment variables

