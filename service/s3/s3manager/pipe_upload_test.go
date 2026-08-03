//go:build go1.8
// +build go1.8

package s3manager_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/request"
	"github.com/IBM/ibm-cos-sdk-go/awstesting/unit"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/IBM/ibm-cos-sdk-go/service/s3/s3manager"
)

// TestUploadWithPipe_Basic tests basic pipe upload functionality
func TestUploadWithPipe_Basic(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("Hello, World! This is test data for pipe upload.")
	var uploadedData []byte
	var mu sync.Mutex

	// Create mock S3 service
	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch data := r.Data.(type) {
		case *s3.CreateMultipartUploadOutput:
			data.UploadId = aws.String("test-upload-id")
		case *s3.UploadPartOutput:
			// Capture uploaded data
			if body, ok := r.Params.(*s3.UploadPartInput).Body.(io.Reader); ok {
				buf := new(bytes.Buffer)
				io.Copy(buf, body)
				uploadedData = append(uploadedData, buf.Bytes()...)
			}
			data.ETag = aws.String("test-etag")
		case *s3.CompleteMultipartUploadOutput:
			data.Location = aws.String("https://test-bucket.s3.amazonaws.com/test-key")
			data.ETag = aws.String("test-etag")
		case *s3.PutObjectOutput:
			// For single part upload
			if body, ok := r.Params.(*s3.PutObjectInput).Body.(io.Reader); ok {
				buf := new(bytes.Buffer)
				io.Copy(buf, body)
				uploadedData = buf.Bytes()
			}
			data.ETag = aws.String("test-etag")
		}

		r.HTTPResponse = &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc)

	// Write data to pipe in a goroutine
	go func() {
		time.Sleep(100 * time.Millisecond) // Give time for pipe to be opened
		pipe, err := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
			t.Errorf("Failed to open pipe for writing: %v", err)
			return
		}
		defer pipe.Close()
		pipe.Write(testData)
	}()

	// Upload from pipe
	result, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Verify uploaded data matches
	if !bytes.Equal(uploadedData, testData) {
		t.Errorf("Uploaded data mismatch. Expected %d bytes, got %d bytes", len(testData), len(uploadedData))
	}
}

// TestUploadWithPipe_LargeFile tests uploading a large file through pipe
func TestUploadWithPipe_LargeFile(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	// Create 10MB of test data
	testDataSize := 10 * 1024 * 1024
	testData := make([]byte, testDataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	var uploadedSize int64
	var partCount int32

	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.CreateMultipartUploadOutput:
			data.UploadId = aws.String("test-upload-id")
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte(`<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><UploadId>test-upload-id</UploadId></InitiateMultipartUploadResult>`))),
			}
		case *s3.UploadPartOutput:
			atomic.AddInt32(&partCount, 1)
			if body, ok := r.Params.(*s3.UploadPartInput).Body.(io.Reader); ok {
				n, _ := io.Copy(ioutil.Discard, body)
				atomic.AddInt64(&uploadedSize, n)
			}
			data.ETag = aws.String(fmt.Sprintf("etag-%d", partCount))
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
				Header:     http.Header{"Etag": []string{fmt.Sprintf("etag-%d", partCount)}},
			}
		case *s3.CompleteMultipartUploadOutput:
			data.Location = aws.String("https://test-bucket.s3.amazonaws.com/test-key")
			data.ETag = aws.String("final-etag")
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte(`<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Location>https://test-bucket.s3.amazonaws.com/test-key</Location><ETag>final-etag</ETag></CompleteMultipartUploadResult>`))),
			}
		default:
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
			}
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc, func(u *s3manager.Uploader) {
		u.PartSize = 5 * 1024 * 1024 // 5MB parts
		u.Concurrency = 3
	})

	// Write data to pipe
	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, err := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
			t.Errorf("Failed to open pipe: %v", err)
			return
		}
		defer pipe.Close()
		pipe.Write(testData)
	}()

	result, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("large-file"),
		},
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Verify multipart upload was used
	if partCount < 2 {
		t.Errorf("Expected multipart upload (>1 parts), got %d parts", partCount)
	}

	// Verify all data was uploaded
	if uploadedSize != int64(testDataSize) {
		t.Errorf("Expected %d bytes uploaded, got %d", testDataSize, uploadedSize)
	}
}

// TestUploadWithPipe_ProgressCallback tests progress tracking
func TestUploadWithPipe_ProgressCallback(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 1024*1024) // 1MB
	var progressCalls int32
	var lastProgress int64

	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.PutObjectOutput:
			data.ETag = aws.String("test-etag")
		}
		r.HTTPResponse = &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc)

	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, _ := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		defer pipe.Close()
		pipe.Write(testData)
	}()

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
		ProgressFn: func(bytesUploaded int64) {
			atomic.AddInt32(&progressCalls, 1)
			atomic.StoreInt64(&lastProgress, bytesUploaded)
		},
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify progress was tracked
	if progressCalls == 0 {
		t.Error("Expected progress callback to be called")
	}

	if lastProgress == 0 {
		t.Error("Expected progress to be tracked")
	}
}

// TestUploadWithPipe_MissingPipePath tests error handling for missing pipe path
func TestUploadWithPipe_MissingPipePath(t *testing.T) {
	svc := s3.New(unit.Session)
	uploader := s3manager.NewUploaderWithClient(svc)

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
	})

	if err == nil {
		t.Fatal("Expected error for missing pipe path")
	}

	if aerr, ok := err.(awserr.Error); ok {
		if aerr.Code() != "InvalidParameter" {
			t.Errorf("Expected InvalidParameter error, got %s", aerr.Code())
		}
	} else {
		t.Error("Expected awserr.Error type")
	}
}

// TestUploadWithPipe_MissingUploadInput tests error handling for missing upload input
func TestUploadWithPipe_MissingUploadInput(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	svc := s3.New(unit.Session)
	uploader := s3manager.NewUploaderWithClient(svc)

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
	})

	if err == nil {
		t.Fatal("Expected error for missing upload input")
	}

	if aerr, ok := err.(awserr.Error); ok {
		if aerr.Code() != "InvalidParameter" {
			t.Errorf("Expected InvalidParameter error, got %s", aerr.Code())
		}
	}
}

// TestUploadWithPipe_NonExistentPipe tests error handling for non-existent pipe
func TestUploadWithPipe_NonExistentPipe(t *testing.T) {
	svc := s3.New(unit.Session)
	uploader := s3manager.NewUploaderWithClient(svc)

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String("/tmp/non-existent-pipe-" + fmt.Sprint(time.Now().UnixNano())),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
	})

	if err == nil {
		t.Fatal("Expected error for non-existent pipe")
	}

	if aerr, ok := err.(awserr.Error); ok {
		if aerr.Code() != "PipeOpenError" {
			t.Errorf("Expected PipeOpenError, got %s", aerr.Code())
		}
	}
}

// TestUploadWithPipe_WithMetadata tests uploading with metadata
func TestUploadWithPipe_WithMetadata(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("test data")
	var capturedMetadata map[string]*string

	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.PutObjectOutput:
			if input, ok := r.Params.(*s3.PutObjectInput); ok {
				capturedMetadata = input.Metadata
			}
			data.ETag = aws.String("test-etag")
		}
		r.HTTPResponse = &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc)

	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, _ := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		defer pipe.Close()
		pipe.Write(testData)
	}()

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
			Metadata: map[string]*string{
				"test-key": aws.String("test-value"),
				"author":   aws.String("test-author"),
			},
		},
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if capturedMetadata == nil {
		t.Fatal("Expected metadata to be captured")
	}

	if aws.StringValue(capturedMetadata["test-key"]) != "test-value" {
		t.Error("Metadata not properly passed through")
	}
}

// TestUploadWithPipe_WithOptions tests upload with custom options
func TestUploadWithPipe_WithOptions(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 20*1024*1024) // 20MB
	var partSize int64
	var concurrency int

	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.CreateMultipartUploadOutput:
			data.UploadId = aws.String("test-upload-id")
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte(`<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><UploadId>test-upload-id</UploadId></InitiateMultipartUploadResult>`))),
			}
		case *s3.UploadPartOutput:
			data.ETag = aws.String("test-etag")
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
				Header:     http.Header{"Etag": []string{"test-etag"}},
			}
		case *s3.CompleteMultipartUploadOutput:
			data.Location = aws.String("https://test-bucket.s3.amazonaws.com/test-key")
			data.ETag = aws.String("final-etag")
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte(`<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Location>https://test-bucket.s3.amazonaws.com/test-key</Location><ETag>final-etag</ETag></CompleteMultipartUploadResult>`))),
			}
		default:
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
			}
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc)

	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, _ := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		defer pipe.Close()
		pipe.Write(testData)
	}()

	_, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
	}, func(u *s3manager.Uploader) {
		u.PartSize = 10 * 1024 * 1024 // 10MB
		u.Concurrency = 2
		partSize = u.PartSize
		concurrency = u.Concurrency
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if partSize != 10*1024*1024 {
		t.Errorf("Expected part size 10MB, got %d", partSize)
	}

	if concurrency != 2 {
		t.Errorf("Expected concurrency 2, got %d", concurrency)
	}
}

// TestUploadWithPipe_EmptyPipe tests uploading from an empty pipe
func TestUploadWithPipe_EmptyPipe(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	svc := s3.New(unit.Session)
	svc.Handlers.Send.Clear()
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.PutObjectOutput:
			data.ETag = aws.String("test-etag")
		}
		r.HTTPResponse = &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
		}
	})

	uploader := s3manager.NewUploaderWithClient(svc)

	// Close pipe immediately (empty data)
	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, _ := os.OpenFile(pipePath, os.O_WRONLY, os.ModeNamedPipe)
		pipe.Close()
	}()

	result, err := uploader.UploadWithPipe(aws.BackgroundContext(), &s3manager.UploadPipeInput{
		PipePath: aws.String(pipePath),
		UploadInput: &s3manager.UploadInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String("test-key"),
		},
	})

	if err != nil {
		t.Fatalf("Expected no error for empty pipe, got %v", err)
	}

	if result == nil {
		t.Fatal("Expected result even for empty upload")
	}
}

// Helper function to create a test named pipe
func createTestPipe(t *testing.T) string {
	tmpDir := os.TempDir()
	pipePath := filepath.Join(tmpDir, fmt.Sprintf("test-pipe-%d", time.Now().UnixNano()))

	if err := syscall.Mkfifo(pipePath, 0666); err != nil {
		t.Fatalf("Failed to create test pipe: %v", err)
	}

	return pipePath
}
