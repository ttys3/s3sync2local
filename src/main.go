package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	appVer = "dev"

	osExit = os.Exit

	wg = &sync.WaitGroup{}

	sizeMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "s3sync",
			Subsystem: "data",
			Name:      "total_size",
			Help:      "Total size of the data in S3",
		},
		[]string{"local_path", "bucket", "bucket_path", "site"},
	)

	objectsMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "s3sync",
			Subsystem: "data",
			Name:      "objects_count",
			Help:      "Number of objects in S3",
		},
		[]string{"local_path", "bucket", "bucket_path", "site"},
	)

	errorsMetric = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "s3sync",
			Subsystem: "errors",
			Name:      "count",
			Help:      "Number of errors",
		},
		[]string{"local_path", "bucket", "bucket_path", "site", "scope"},
	)
)

// DownloadCFG - structure for the download queue
type DownloadCFG struct {
	s3Service *s3.S3
	key       string
	site      Site
	action    string
	wg *sync.WaitGroup
}

// ChecksumCFG - structure for the checksum comparison queue
type ChecksumCFG struct {
	DownloadCFG    DownloadCFG
	filename       string
	key			   string
	checksumRemote string
	site           Site
	wg *sync.WaitGroup
}

func main() {
	var configpath string
	var metricsPort string
	var metricsPath string
	var showVerOnly bool

	// Read command line args
	flag.StringVar(&configpath, "config", "config.yml", "Path to the config.yml")
	flag.StringVar(&metricsPort, "metrics-port", "0", "Prometheus exporter port, 0 to disable the exporter")
	flag.StringVar(&metricsPath, "metrics-path", "/metrics", "Prometheus exporter path")
	flag.BoolVar(&showVerOnly, "v", false, "show app version")
	flag.Parse()

	fmt.Printf("s3sync2local %s\n", appVer)

	if showVerOnly {
		osExit(0)
	}

	// Read config key
	config := readConfigFile(configpath)

	// init logger
	initLogger(config)

	// Start prometheus exporter
	if metricsPort != "0" {
		go prometheusExporter(metricsPort, metricsPath)
	}

	// Set global WatchInterval
	if config.WatchInterval == 0 {
		config.WatchInterval = 1000 * 300
	}

	// Init download workers num
	if config.DownloadWorkers == 0 {
		config.DownloadWorkers = 10
	}

	var cancel context.CancelFunc
	ctx := context.Background()
	ctx, cancel = context.WithCancel(ctx)

	var cancelChk context.CancelFunc
	ctxChk := context.Background()
	ctxChk, cancelChk = context.WithCancel(ctxChk)

	defer func() {
		logger.Infof("cancel all workers")
		cancelChk()
		cancel()
		time.Sleep(time.Millisecond * 300)
		logger.Infof("all done")
	}()

	downloadCh := make(chan DownloadCFG, config.DownloadQueueBuffer)
	logger.Infof("starting %s download workers", strconv.Itoa(config.DownloadWorkers))
	for x := 0; x < config.DownloadWorkers; x++ {
		go downloadWorker(ctx, downloadCh, x)
	}

	// Init checksum checker workers
	if config.ChecksumWorkers == 0 {
		config.ChecksumWorkers = runtime.NumCPU() * 2
	}

	checksumCh := make(chan ChecksumCFG)
	logger.Infof("starting %s checksum workers", strconv.Itoa(config.ChecksumWorkers))
	for x := 0; x < config.ChecksumWorkers; x++ {
		go checksumWorker(ctxChk, checksumCh, downloadCh, x)
	}

	// Start separate thread for each site
	for _, site := range config.Sites {
		// Remove leading slash from the BucketPath
		site.BucketPath = strings.TrimLeft(site.BucketPath, "/")
		// Set site name
		if site.Name == "" {
			site.Name = site.Bucket + "/" + site.BucketPath
		}
		// Set site Endpoint
		if site.Endpoint == "" {
			site.Endpoint = config.Endpoint
		}
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

		logger.Infof("==== starting sync for site: %s ====", site.Name)
		wg.Add(1)
		go syncSite(site, downloadCh, checksumCh, wg)
	}
	wg.Wait()
}

func prometheusExporter(metricsPort string, metricsPath string) {
	http.Handle(metricsPath, promhttp.Handler())
	http.ListenAndServe(":"+metricsPort, nil)
}

func downloadWorker(ctx context.Context, downloadCh <-chan DownloadCFG, idx int) {
	for {
		select {
		case cfg := <-downloadCh:
			if cfg.action == "download" {
				downloadFile(cfg.key, cfg.site)
			} else if cfg.action == "delete" {
				deleteFile(cfg.key, cfg.site)
			} else {
				logger.Errorf("programming error, unknown action: %s", cfg.action)
			}
			// tell sender we have done job
			cfg.wg.Done()
		case <-ctx.Done():
			logger.Debugf("downloadWorker %d exited", idx)
			return
		}
	}
}

func checksumWorker(ctx context.Context, checksumCh <-chan ChecksumCFG, downloadCh chan<- DownloadCFG, idx int) {
	for {
		select {
		case cfg := <-checksumCh:
			filename := compareChecksum(cfg.filename, cfg.checksumRemote, cfg.site)
			if len(filename) > 0 {
				// Add key to the download queue
				downloadCh <- cfg.DownloadCFG
			} else {
				cfg.DownloadCFG.wg.Done()
			}
			// tell sender we have done job
			cfg.wg.Done()
		case <-ctx.Done():
			logger.Debugf("checksumWorker %d exited", idx)
			return
		}
	}
}
