package main

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"

	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/schollz/progressbar/v3"
)

func checkIfExcluded(path string, exclusions []string) bool {
	excluded := false

	for _, exclusion := range exclusions {
		re := regexp.MustCompile(exclusion)
		if re.FindAll([]byte(path), -1) != nil {
			excluded = true
		}
	}

	return excluded
}

// FilePathWalkDir walks through the directory and all subdirectories returning list of files for download (local not exists)
// and list of files to be deleted from local (not exists from S3, but local exists)
func FilePathWalkDir(site Site, awsItems map[string]string, s3Service *s3.S3, donwloadCh chan<- DownloadCFG, checksumCh chan<- ChecksumCFG, bar *progressbar.ProgressBar) {
	wg := &sync.WaitGroup{}
	// only walk local_path + bucket_path
	err := filepath.Walk(filepath.Join(site.LocalPath, site.BucketPath), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Update errors metric
			errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
			logger.Error(err)
			return err
		}

		if !info.IsDir() {
			s3Key := generateS3Key(site.LocalPath, path)
			// remove local not exists
			if awsItems[s3Key] == "" {
				if site.RetireDeleted {
					wg.Add(1)
					donwloadCh <- DownloadCFG{s3Service, s3Key, site, "delete", wg, bar}
				}
			} else {
				wg.Add(1)
				checksumRemote := awsItems[s3Key]
				checksumCh <- ChecksumCFG{DownloadCFG{s3Service, s3Key, site, "download", wg, bar}, path, s3Key, checksumRemote, site}
			}
		}
		return nil
	})

	// Check for not downloaded files
	for key := range awsItems {
		// Replace: strip BucketPath from key
		localPath := generateLocalpath(site.LocalPath, key)
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			wg.Add(1)
			donwloadCh <- DownloadCFG{s3Service, key, site, "download", wg, bar}
		}
	}

	if err != nil {
		// Update errors metric
		errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
		logger.Error(err)
	}

	wg.Wait()
	logger.Debugf("FilePathWalkDir return")
}

func compareChecksum(filename string, checksumRemote string, site Site) string {
	var sumOfSums []byte
	var parts int
	var finalSum []byte
	chunkSize := int64(5 * 1024 * 1024)

	logger.Debugf("%s: comparing checksums", filename)

	if filename == "" {
		return filename
	}

	file, err := os.Open(filename)
	if err != nil {
		// Update errors metric
		errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
		logger.Error(err)
		return filename
	}
	defer file.Close()

	dataSize, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		// Update errors metric
		errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
		logger.Error(err)
		return ""
	}

	for start := int64(0); start < dataSize; start += chunkSize {
		length := chunkSize
		if start+chunkSize > dataSize {
			length = dataSize - start
		}
		sum, err := chunkMd5Sum(file, start, length)
		if err != nil {
			// Update errors metric
			errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
			logger.Error(err)
			return ""
		}
		sumOfSums = append(sumOfSums, sum...)
		parts++
	}

	if parts == 1 {
		finalSum = sumOfSums
	} else {
		h := md5.New()
		_, err := h.Write(sumOfSums)
		if err != nil {
			// Update errors metric
			errorsMetric.WithLabelValues(site.LocalPath, site.Bucket, site.BucketPath, site.Name, "local").Inc()
			logger.Error(err)
			return ""
		}
		finalSum = h.Sum(nil)
	}

	sumHex := hex.EncodeToString(finalSum)

	if parts > 1 {
		sumHex += "-" + strconv.Itoa(parts)
	}

	if sumHex != checksumRemote {
		logger.Debugf("%s: checksums do not match, local checksum is %s, remote - %s", filename, sumHex, checksumRemote)
		return filename
	}
	logger.Debugf("%s: checksums matched", filename)
	return ""
}

func chunkMd5Sum(file io.ReadSeeker, start int64, length int64) ([]byte, error) {
	file.Seek(start, io.SeekStart)
	h := md5.New()
	if _, err := io.CopyN(h, file, length); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}
