package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/dustin/go-humanize"
	"github.com/schollz/progressbar/v3"
)

func getObjectSize(s3Service *s3.S3, site Site, s3Key string) int64 {
	objSize := int64(0)

	params := &s3.ListObjectsInput{
		Bucket: aws.String(site.Bucket),
		Prefix: aws.String(s3Key),
	}

	// Get object size prior deletion
	obj, objErr := s3Service.ListObjects(params)
	if objErr == nil {
		for _, s3obj := range obj.Contents {
			objSize = *s3obj.Size
		}
	}

	return objSize
}

func generateS3Key(localPath string, filePath string) string {
	relativePath, _ := filepath.Rel(localPath, filePath)
	if runtime.GOOS == "windows" {
		relativePath = strings.ReplaceAll(relativePath, "\\", "/")
	}
	return relativePath
}

func generateLocalpath(localPath string, key string) string {
	return filepath.Join(localPath, key)
}

func getS3Session(site Site) *session.Session {
	config := aws.Config{
		Region:     aws.String(site.BucketRegion),
		Endpoint:   aws.String(site.Endpoint),
		MaxRetries: aws.Int(-1),
	}

	if site.AccessKey != "" && site.SecretAccessKey != "" {
		config.Credentials = credentials.NewStaticCredentials(site.AccessKey, site.SecretAccessKey, "")
	}

	return session.Must(session.NewSession(&config))
}

func getS3Service(site Site) *s3.S3 {
	return s3.New(getS3Session(site))
}

func getAwsS3ItemMap(s3Service *s3.S3, site Site) (map[string]string, error) {
	var items = make(map[string]string)

	perpage := int64(1024)
	params := &s3.ListObjectsV2Input{
		Bucket: aws.String(site.Bucket),
		Prefix: aws.String(site.BucketPath),
		MaxKeys: aws.Int64(perpage),
	}

	logger.Infof("[%s] begin list objects ...", site.Name)

	bar := progressbar.Default(5000, fmt.Sprintf("list objects [%d/page] ...", perpage))

	err := s3Service.ListObjectsV2Pages(params,
		func(page *s3.ListObjectsV2Output, last bool) bool {
			// Process the objects for each page
			for _, s3obj := range page.Contents {
				if aws.StringValue(s3obj.StorageClass) != site.StorageClass {
					logger.Warnf("storage class does not match, marking for not download: %s", aws.StringValue(s3obj.Key))
				} else {
					// Update metrics
					sizeMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name).Add(float64(*s3obj.Size))
					objectsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name).Inc()
					items[aws.StringValue(s3obj.Key)] = strings.Trim(*(s3obj.ETag), "\"")
				}
			}
			bar.Add(1)
			return true
		},
	)

	bar.Finish()
	logger.Infof("[%s] done list objects", site.Name)

	if err != nil {
		// Update errors metric
		errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "cloud").Inc()
		logger.Errorf("Error listing %s objects: %s", *params.Bucket, err)
		return nil, err
	}
	return items, nil
}

func downloadFile(key string, site Site) {
	localpath := generateLocalpath(site.LocalPath, key)
	localDir := filepath.Dir(localpath)
	if _, err := os.Stat(localDir); os.IsNotExist(err) {
		if err := os.MkdirAll(localDir, os.ModePerm); err != nil {
			errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
			logger.Errorf("failed to create dir %q, %v", localDir, err)
			return
		}
	}
	// local key to save
	f, fileErr := os.Create(localpath)

	// Try to get object size in case we updating already existing
	//objSize := getObjectSize(s3Service, site, localpath)

	if fileErr != nil {
		// Update errors metric
		errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
		logger.Errorf("failed to create file %q, %v", localpath, fileErr)
	} else {
		defer f.Close()
		downloader := s3manager.NewDownloader(getS3Session(site), func(u *s3manager.Downloader) {
			u.PartSize = 5 * 1024 * 1024
			u.Concurrency = 5
		})
		n, err := downloader.Download(f, &s3.GetObjectInput{
			Bucket:       aws.String(site.Bucket),
			Key:          aws.String(key),
		})

		if err != nil {
			// Update errors metric
			errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "cloud").Inc()
			logger.Errorf("failed to download object: b:%s, k:%s => %s, err %v", site.Bucket, key, localpath, err)
		} else {
			donwloadSizeCounter.Add(uint64(n))
			logger.Debugf("successfully downloaded object to: %s", localpath)
		}
	}
}

func deleteFile(s3Key string, site Site) {
	localfile := generateLocalpath(site.LocalPath, s3Key)
	if err := os.Remove(localfile); err != nil {
		logger.Errorf("removed local file failed: %s, b:%s, k:%s => %s", err, site.Bucket, s3Key, localfile)
	} else {
		logger.Debugf("removed s3 object: b:%s, k:%s => %s", site.Bucket, s3Key, localfile)
	}
}

func syncSite(site Site, downloadCh chan<- DownloadCFG, checksumCh chan<- ChecksumCFG, wg *sync.WaitGroup) {
	// Initi S3 session
	s3Service := s3.New(getS3Session(site))
	// Watch directory for realtime sync
	//go watch(s3Service, site, downloadCh)
	// Fetch S3 objects
	awsItems, err := getAwsS3ItemMap(s3Service, site)
	if err != nil {
		logger.Errorln(err)
		osExit(4)
	} else {
		logger.Infof("[%s] begin sync ...", site.Name)
		bar := progressbar.Default(int64(len(awsItems)), "sync...")
		// Compare S3 objects with local
		FilePathWalkDir(site, awsItems, s3Service, downloadCh, checksumCh, bar)
		bar.Finish()
		logger.Infof("[%s] finished sync. downloaded local files: %d, downloaded total size: %s, deleted local files: %d",
			site.Name,
			donwloadCounter.Count(),
			humanize.Bytes(uint64(donwloadSizeCounter.Count())),
		deletedCounter.Count())
	}
	wg.Done()
}
