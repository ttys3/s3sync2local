package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/schollz/progressbar/v3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	appVer = "dev"

	osExit = os.Exit

	hasShutdown = false
	hasShutdownLock = sync.RWMutex{}

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

	donwloadCounter = NewCounter()
	donwloadSizeCounter = NewCounter()
	deletedCounter = NewCounter()
)

// DownloadCFG - structure for the download queue
type DownloadCFG struct {
	s3Service *s3.S3
	key       string
	site      Site
	action    string
	wg *sync.WaitGroup
	bar *progressbar.ProgressBar
}

// ChecksumCFG - structure for the checksum comparison queue
type ChecksumCFG struct {
	DownloadCFG    DownloadCFG
	filename       string
	key			   string
	checksumRemote string
	site           Site
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

	fmt.Printf("\t==== s3sync2local %s ====\n", appVer)
	fmt.Printf("\t====     荒野無燈 https://ttys3.net       ====\n")

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

	var canelSite context.CancelFunc
	ctxSite := context.Background()
	ctxSite, canelSite = context.WithCancel(ctxSite)

	defer func() {
		logger.Debugf("defer called")
		shutdown(canelSite, cancelChk, cancel)
	}()

	setupSigTermHandler(func() {
		logger.Debugf("sig handler called")
		shutdown(canelSite, cancelChk, cancel)
	})

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

		if bucketPath, err := parseBucketPath(site.BucketPath); err != nil {
			logger.Fatal(err)
		} else {
			site.BucketPath = bucketPath
			logger.Infof("parsed bucket_path: %s", site.BucketPath)
		}

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

		wg.Add(1)
		go syncSite(ctxSite, site, downloadCh, checksumCh, wg)
	}
	wg.Wait()
	//fmt.Println("now app real die")
}

func parseBucketPath(bucketPath string) (string, error) {
	if !strings.Contains(bucketPath, "{{") {
		return bucketPath, nil
	}
	t := template.Must(template.New("bucket_path").Parse(bucketPath))
	buf := bytes.NewBufferString("")
	yesterday := time.Now().Add(-24*time.Hour)
	if err := t.Execute(buf, struct {
		Year  string
		Month string
		Day string
	}{
		fmt.Sprintf("%02d", yesterday.Year()),
		fmt.Sprintf("%02d", yesterday.Month()),
		fmt.Sprintf("%02d", yesterday.Day()),
	}); err != nil {
		return "", fmt.Errorf("invalid bucket_path")
	}
	return buf.String(), nil
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
			cfg.bar.Add(1)
			// tell sender we have done job
			cfg.wg.Done()
		case <-ctx.Done():
			logger.Tracef("downloadWorker %d exited", idx)
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
				cfg.DownloadCFG.bar.Add(1)
				cfg.DownloadCFG.wg.Done()
			}
		case <-ctx.Done():
			logger.Tracef("checksumWorker %d exited", idx)
			return
		}
	}
}

func setupSigTermHandler(handler func()) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Infof("\n- Ctrl+C pressed in terminal, stopping ...")
		handler()
		// no need to time.Sleep here, because the main goroutine may have returned at this time
		// this info may not have been printed
		logger.Infof("\n- stopped")
		os.Exit(0)
	}()
}

func shutdown(handlers... func()) {
	// because in our condition, when syncSite() go routine exited, main goroutine has enough time to return, thus the defer will also be called
	// add lock to prevent duplicate call to shutdown() in both defer and setupSigTermHandler()
	hasShutdownLock.RLock()
	if hasShutdown {
		hasShutdownLock.RUnlock()
		logger.Debugf("shutdown() already been called, just return")
		return
	} else {
		hasShutdownLock.RUnlock()
	}
	// lock and block the call to shutdown() until the job is done
	hasShutdownLock.Lock()

	logger.Infof("shutdown: stopping all background jobs ...")
	for _, hanlder := range handlers {
		hanlder()
	}
	// wait goroutine receiving the msg, and in this waiting time,
	// warn: the main goroutine may have returned, so we use a exitCh in setupSigTermHandler
	time.Sleep(time.Millisecond * 300)
	logger.Infof("shutdown: all done")
	hasShutdown = true
	hasShutdownLock.Unlock()
}