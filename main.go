package main

import (
	"flag"
	"net/http"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	wg = sync.WaitGroup{}

	sizeMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "s3sync",
			Name:      "s3sync_data_total_size",
			Help:      "Total size of the data in S3",
		},
		[]string{"local_path", "bucket", "bucket_path"},
	)

	objectsMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "s3sync",
			Name:      "s3sync_data_objects_count",
			Help:      "Nember of objects in S3",
		},
		[]string{"local_path", "bucket", "bucket_path"},
	)
)

// UploadCFG - structure for the upload queue
type UploadCFG struct {
	s3Service *s3.S3
	file      string
	site      Site
	action    string
}

// ChecksumCFG - structure for the checksum comparison queue
type ChecksumCFG struct {
	UploadCFG      UploadCFG
	filename       string
	checksumRemote string
}

func main() {
	var config Config
	var configpath string
	var prometheusport string

	// Read command line args
	flag.StringVar(&configpath, "config", "config.yml", "Path to the config.yml")
	flag.StringVar(&prometheusport, "port", "9200", "Prometheus exporter port")
	flag.Parse()

	// Read config file
	readFile(&config, configpath)

	// init logger
	initLogger(config)

	// Start prometheus exporter
	go prometheusExporter(prometheusport)

	// Set global WatchInterval
	if config.WatchInterval == 0 {
		config.WatchInterval = 1000
	}

	// Init upload worker
	if config.UploadWorkers == 0 {
		config.UploadWorkers = 10
	}

	uploadCh := make(chan UploadCFG, config.UploadQueueBuffer)
	for x := 0; x < config.UploadWorkers; x++ {
		go uploadWorker(uploadCh)
	}

	// Init checksum checker workers
	checksumCh := make(chan ChecksumCFG)
	for x := 0; x < 20; x++ {
		go checksumWorker(checksumCh, uploadCh)
	}

	// Start separate thread for each site
	wg.Add(len(config.Sites))
	for _, site := range config.Sites {
		// Remove leading slash from the BucketPath
		site.BucketPath = strings.TrimLeft(site.BucketPath, "/")
		// Set site AccessKey
		if site.AccessKey == "" {
			site.AccessKey = config.AccessKey
		}
		// Set site SecretAccessKey
		if site.SecretAccessKey == "" {
			site.SecretAccessKey = config.SecretAccessKey
		}
		// Set site BucketRegion
		if site.BucketRegion == "" {
			site.BucketRegion = config.AwsRegion
		}
		// Set default value for StorageClass
		if site.StorageClass == "" {
			site.StorageClass = "STANDARD"
		}
		// Set site WatchInterval
		if site.WatchInterval == 0 {
			site.WatchInterval = config.WatchInterval
		}
		go syncSite(site, uploadCh, checksumCh)
	}
	wg.Wait()
}

func prometheusExporter(prometheusport string) {
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":"+prometheusport, nil)
}

func uploadWorker(uploadCh <-chan UploadCFG) {
	for cfg := range uploadCh {
		if cfg.action == "upload" {
			uploadFile(cfg.s3Service, cfg.file, cfg.site)
		} else if cfg.action == "delete" {
			deleteFile(cfg.s3Service, cfg.file, cfg.site)
		} else {
			logger.Errorf("programming error, unknown action: %s", cfg.action)
		}
	}
}

func checksumWorker(checksumCh <-chan ChecksumCFG, uploadCh chan<- UploadCFG) {
	for cfg := range checksumCh {
		filename := compareChecksum(cfg.filename, cfg.checksumRemote)
		if len(filename) > 0 {
			// Add file to the upload queue
			uploadCh <- cfg.UploadCFG
		}
	}
}
