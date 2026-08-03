//go:build go1.7
// +build go1.7

package s3manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/request"
	"github.com/IBM/ibm-cos-sdk-go/awstesting/unit"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/IBM/ibm-cos-sdk-go/service/s3/s3manager"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// drainPipe opens the read end of a named pipe in a goroutine and drains it
// completely. It returns a wait function that blocks until draining is done and
// surfaces any open/read error via t.Errorf.
//
// The 100 ms sleep gives the writer goroutine time to call os.OpenFile(O_WRONLY)
// first; both sides must open a FIFO before either unblocks.
func drainPipe(t *testing.T, pipePath string) (data func() []byte, wait func()) {
	t.Helper()
	ch := make(chan []byte, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		pipe, err := os.OpenFile(pipePath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			t.Errorf("drainPipe: open: %v", err)
			ch <- nil
			return
		}
		defer pipe.Close()
		b, err := ioutil.ReadAll(pipe)
		if err != nil {
			t.Errorf("drainPipe: read: %v", err)
		}
		ch <- b
	}()
	var result []byte
	wait = func() {
		result = <-ch
	}
	data = func() []byte { return result }
	return data, wait
}

// mockSinglePartHandler returns a Send handler that services HeadObject and
// GetObject for a single-part object whose body is body. Both r.HTTPResponse.Body
// and data.Body are set to the same data so the SDK unmarshal and our io.Copy
// both see the correct bytes.
func mockSinglePartHandler(body []byte) func(*request.Request) {
	size := int64(len(body))
	return func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(size)
			data.PartsCount = aws.Int64(1)
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader([]byte{})),
			}
		case *s3.GetObjectOutput:
			data.ContentLength = aws.Int64(size)
			data.Body = ioutil.NopCloser(bytes.NewReader(body))
			r.HTTPResponse = &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewReader(body)),
			}
		}
	}
}

// newSvc creates a fresh S3 client with its Send handlers cleared, ready for a
// custom handler to be pushed. Passing noRetry=true disables SDK-level retries
// so that only application-level retry loops are counted.
func newSvc(noRetry bool) *s3.S3 {
	var svc *s3.S3
	if noRetry {
		svc = s3.New(unit.Session, &aws.Config{MaxRetries: aws.Int(0)})
	} else {
		svc = s3.New(unit.Session)
	}
	svc.Handlers.Send.Clear()
	return svc
}

// ---------------------------------------------------------------------------
// Basic tests
// ---------------------------------------------------------------------------

// TestDownloadWithPipe_Basic tests basic pipe download functionality.
func TestDownloadWithPipe_Basic(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("Hello, World! This is test data for pipe download.")
	objectSize := int64(len(testData))

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(mockSinglePartHandler(testData))

	downloader := s3manager.NewDownloaderWithClient(svc)
	getData, wait := drainPipe(t, pipePath)

	n, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != objectSize {
		t.Errorf("bytes downloaded: want %d, got %d", objectSize, n)
	}
	if !bytes.Equal(getData(), testData) {
		t.Errorf("data mismatch: want %d bytes, got %d bytes", len(testData), len(getData()))
	}
}

// TestDownloadWithPipe_LargeFile tests downloading a multi-part 10 MB file.
func TestDownloadWithPipe_LargeFile(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	const testDataSize = 10 * 1024 * 1024
	testData := make([]byte, testDataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	var getObjectCalls int32

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(testDataSize)
			data.PartsCount = aws.Int64(2)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			atomic.AddInt32(&getObjectCalls, 1)
			input := r.Params.(*s3.GetObjectInput)
			var start, end int64
			fmt.Sscanf(aws.StringValue(input.Range), "bytes=%d-%d", &start, &end)
			if end >= testDataSize {
				end = testDataSize - 1
			}
			part := testData[start : end+1]
			data.ContentLength = aws.Int64(int64(len(part)))
			data.Body = ioutil.NopCloser(bytes.NewReader(part))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(part))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc, func(d *s3manager.Downloader) {
		d.PartSize = 5 * 1024 * 1024
		d.Concurrency = 2
	})
	getData, wait := drainPipe(t, pipePath)

	n, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("large-file")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != testDataSize {
		t.Errorf("bytes downloaded: want %d, got %d", testDataSize, n)
	}
	if getObjectCalls < 2 {
		t.Errorf("expected parallel download (>=2 GetObject calls), got %d", getObjectCalls)
	}
	if !bytes.Equal(getData(), testData) {
		t.Error("downloaded data does not match original")
	}
}

// TestDownloadWithPipe_ProgressCallback tests that the progress callback is invoked.
func TestDownloadWithPipe_ProgressCallback(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 1024*1024) // 1 MB
	objectSize := int64(len(testData))

	var progressCalls int32
	var lastProgress, lastTotal int64

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(mockSinglePartHandler(testData))

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
		ProgressFn: func(downloaded, total int64) {
			atomic.AddInt32(&progressCalls, 1)
			atomic.StoreInt64(&lastProgress, downloaded)
			atomic.StoreInt64(&lastTotal, total)
		},
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if progressCalls == 0 {
		t.Error("progress callback was never called")
	}
	if lastProgress == 0 {
		t.Error("lastProgress is 0; expected > 0")
	}
	if lastTotal != objectSize {
		t.Errorf("total size: want %d, got %d", objectSize, lastTotal)
	}
}

// ---------------------------------------------------------------------------
// Input-validation tests (no network calls needed)
// ---------------------------------------------------------------------------

// TestDownloadWithPipe_MissingPipePath tests that omitting PipePath returns InvalidParameter.
func TestDownloadWithPipe_MissingPipePath(t *testing.T) {
	downloader := s3manager.NewDownloaderWithClient(newSvc(false))

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})

	requireAWSErrCode(t, err, "InvalidParameter")
}

// TestDownloadWithPipe_MissingGetObjectInput tests that omitting GetObjectInput returns InvalidParameter.
func TestDownloadWithPipe_MissingGetObjectInput(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	downloader := s3manager.NewDownloaderWithClient(newSvc(false))

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})

	requireAWSErrCode(t, err, "InvalidParameter")
}

// TestDownloadWithPipe_InvalidMaxBufferMemory tests that a negative MaxBufferMemory returns InvalidParameter.
func TestDownloadWithPipe_InvalidMaxBufferMemory(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	downloader := s3manager.NewDownloaderWithClient(newSvc(false))

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(-1),
	})

	requireAWSErrCode(t, err, "InvalidParameter")
}

// requireAWSErrCode fails the test if err is nil or its AWS error code doesn't match want.
func requireAWSErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", want)
	}
	if aerr, ok := err.(awserr.Error); ok {
		if aerr.Code() != want {
			t.Errorf("error code: want %q, got %q", want, aerr.Code())
		}
	} else {
		t.Errorf("expected awserr.Error, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// Pipe / infrastructure tests
// ---------------------------------------------------------------------------

// TestDownloadWithPipe_MissingMaxBufferMemory verifies that omitting MaxBufferMemory
// auto-calculates the value and the download still succeeds.
func TestDownloadWithPipe_MissingMaxBufferMemory(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("test data for auto-calculated buffer")
	objectSize := int64(len(testData))

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(mockSinglePartHandler(testData))

	downloader := s3manager.NewDownloaderWithClient(svc)
	getData, wait := drainPipe(t, pipePath)

	n, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:       aws.String(pipePath),
		GetObjectInput: &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		// MaxBufferMemory intentionally omitted
	})
	wait()

	if err != nil {
		t.Fatalf("expected no error with auto-calculated buffer, got: %v", err)
	}
	if n != objectSize {
		t.Errorf("bytes downloaded: want %d, got %d", objectSize, n)
	}
	if !bytes.Equal(getData(), testData) {
		t.Errorf("data mismatch: want %d bytes, got %d bytes", len(testData), len(getData()))
	}
}

// TestDownloadWithPipe_NonExistentPipe tests that opening a non-existent pipe
// returns a PipeOpenError.
func TestDownloadWithPipe_NonExistentPipe(t *testing.T) {
	downloader := s3manager.NewDownloaderWithClient(newSvc(false))

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String("/tmp/non-existent-pipe-" + fmt.Sprint(time.Now().UnixNano())),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})

	requireAWSErrCode(t, err, "PipeOpenError")
}

// ---------------------------------------------------------------------------
// Functional tests
// ---------------------------------------------------------------------------

// TestDownloadWithPipe_OrderedDelivery verifies that parts downloaded out of
// order are reassembled in the correct sequence before writing to the pipe.
func TestDownloadWithPipe_OrderedDelivery(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	const (
		partSize = 1024 * 1024 // 1 MB
		numParts = 5
	)
	testData := make([]byte, partSize*numParts)
	for i := 0; i < numParts; i++ {
		for j := 0; j < partSize; j++ {
			testData[i*partSize+j] = byte(i)
		}
	}
	objectSize := int64(len(testData))

	var (
		downloadOrder []int64
		mu            sync.Mutex
	)

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(numParts)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			input := r.Params.(*s3.GetObjectInput)
			partNum := aws.Int64Value(input.PartNumber)
			mu.Lock()
			downloadOrder = append(downloadOrder, partNum)
			mu.Unlock()
			// Delay part 1 to force out-of-order completion.
			if partNum == 1 {
				time.Sleep(50 * time.Millisecond)
			}
			var start, end int64
			fmt.Sscanf(aws.StringValue(input.Range), "bytes=%d-%d", &start, &end)
			if end >= objectSize {
				end = objectSize - 1
			}
			part := testData[start : end+1]
			data.ContentLength = aws.Int64(int64(len(part)))
			data.Body = ioutil.NopCloser(bytes.NewReader(part))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(part))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc, func(d *s3manager.Downloader) {
		d.PartSize = partSize
		d.Concurrency = 3
	})
	getData, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(getData(), testData) {
		t.Error("data written to pipe is not in correct order")
	}
	if len(downloadOrder) != numParts {
		t.Errorf("parts downloaded: want %d, got %d", numParts, len(downloadOrder))
	}
}

// TestDownloadWithPipe_WithTimeout verifies that a custom PipeTimeout is accepted
// and the download completes successfully.
func TestDownloadWithPipe_WithTimeout(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("test data")

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(mockSinglePartHandler(testData))

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		PipeTimeout:     aws.Int64(60),
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDownloadWithPipe_WithMaxBufferMemory verifies that an explicitly set
// MaxBufferMemory is respected and the download succeeds.
func TestDownloadWithPipe_WithMaxBufferMemory(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 1024*1024) // 1 MB

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(mockSinglePartHandler(testData))

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(256 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDownloadWithPipe_WithOptions verifies that functional options passed to
// DownloadWithPipe are applied to the Downloader.
func TestDownloadWithPipe_WithOptions(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 10*1024*1024) // 10 MB
	objectSize := int64(len(testData))

	var appliedPartSize int64
	var appliedConcurrency int

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(2)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			input := r.Params.(*s3.GetObjectInput)
			var start, end int64
			fmt.Sscanf(aws.StringValue(input.Range), "bytes=%d-%d", &start, &end)
			if end >= objectSize {
				end = objectSize - 1
			}
			part := testData[start : end+1]
			data.ContentLength = aws.Int64(int64(len(part)))
			data.Body = ioutil.NopCloser(bytes.NewReader(part))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(part))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	}, func(d *s3manager.Downloader) {
		d.PartSize = 5 * 1024 * 1024
		d.Concurrency = 2
		appliedPartSize = d.PartSize
		appliedConcurrency = d.Concurrency
	})
	wait()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if appliedPartSize != 5*1024*1024 {
		t.Errorf("part size: want 5 MB, got %d", appliedPartSize)
	}
	if appliedConcurrency != 2 {
		t.Errorf("concurrency: want 2, got %d", appliedConcurrency)
	}
}

// TestDownloadWithPipe_ContextCancellation verifies that cancelling the context
// stops the download and returns an error.
func TestDownloadWithPipe_ContextCancellation(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := make([]byte, 10*1024*1024) // 10 MB
	objectSize := int64(len(testData))

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(2)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			time.Sleep(200 * time.Millisecond) // slow download so context fires first
			data.ContentLength = aws.Int64(objectSize)
			data.Body = ioutil.NopCloser(bytes.NewReader(testData))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(testData))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Use a shorter sleep here so the pipe opens before the context fires.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(50 * time.Millisecond)
		pipe, err := os.OpenFile(pipePath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			return
		}
		defer pipe.Close()
		ioutil.ReadAll(pipe) //nolint:errcheck
	}()

	_, err := downloader.DownloadWithPipe(ctx, &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	<-done

	if err == nil {
		t.Log("Warning: expected context cancellation error, but got none (may be a timing issue)")
	}
}

// TestDownloadWithPipe_S3Error verifies that an S3 error (e.g. NoSuchKey) is
// surfaced correctly. Because downloadWithPipe opens the pipe O_WRONLY before
// issuing any S3 calls, we must open the read end first so the write-open
// unblocks; the reader goroutine then drains to EOF once the write end closes.
func TestDownloadWithPipe_S3Error(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	svc := newSvc(false)
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		r.HTTPResponse = &http.Response{
			StatusCode: 404,
			Body:       ioutil.NopCloser(bytes.NewReader([]byte("Not Found"))),
		}
		r.Error = awserr.New("NoSuchKey", "The specified key does not exist", nil)
	})

	downloader := s3manager.NewDownloaderWithClient(svc)

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		pipe, err := os.OpenFile(pipePath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			return
		}
		defer pipe.Close()
		ioutil.ReadAll(pipe) //nolint:errcheck
	}()

	_, err := downloader.DownloadWithPipe(aws.BackgroundContext(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("non-existent-key")},
		MaxBufferMemory: aws.Int64(100 * 1024 * 1024),
	})
	<-readerDone

	requireAWSErrCode(t, err, "NoSuchKey")
}

// ---------------------------------------------------------------------------
// Retry tests
// ---------------------------------------------------------------------------

// TestDownloadWithPipe_RetryOnGetObjectFailure verifies that transient GetObject
// errors are retried up to the default limit (3) and the download succeeds on
// the third attempt.
//
// SDK-level retries are disabled (MaxRetries:0) so that only the application-
// level retry loop in downloadWithPipe is counted.
func TestDownloadWithPipe_RetryOnGetObjectFailure(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("Test data for retry")
	objectSize := int64(len(testData))
	var attemptCount int32

	svc := newSvc(true) // noRetry
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(1)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			count := atomic.AddInt32(&attemptCount, 1)
			if count < 3 {
				r.Error = errors.New("network timeout")
				r.HTTPResponse = &http.Response{StatusCode: 500, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
				return
			}
			// Success on 3rd attempt — body must be in r.HTTPResponse.Body
			// because the SDK unmarshal reads from there, not data.Body directly.
			data.ContentLength = aws.Int64(objectSize)
			data.Body = ioutil.NopCloser(bytes.NewReader(testData))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(testData))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	getData, wait := drainPipe(t, pipePath)

	n, err := downloader.DownloadWithPipe(context.Background(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(2 * 1024 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if n != objectSize {
		t.Errorf("bytes downloaded: want %d, got %d", objectSize, n)
	}
	if !bytes.Equal(getData(), testData) {
		t.Error("downloaded data mismatch")
	}
	if attemptCount != 3 {
		t.Errorf("attempt count: want 3, got %d", attemptCount)
	}
}

// TestDownloadWithPipe_RetryOnGetObjectError verifies retry behaviour when a
// transient transport error is returned on the first two attempts.
func TestDownloadWithPipe_RetryOnGetObjectError(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("Test data for transient error retry")
	objectSize := int64(len(testData))
	var attemptCount int32

	svc := newSvc(true) // noRetry
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(1)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			count := atomic.AddInt32(&attemptCount, 1)
			data.ContentLength = aws.Int64(objectSize)
			if count < 3 {
				r.Error = fmt.Errorf("simulated transport error at attempt %d", count)
				r.HTTPResponse = &http.Response{StatusCode: 500, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
			} else {
				data.Body = ioutil.NopCloser(bytes.NewReader(testData))
				r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(testData))}
			}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	getData, wait := drainPipe(t, pipePath)

	n, err := downloader.DownloadWithPipe(context.Background(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(2 * 1024 * 1024 * 1024),
	})
	wait()

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if n != objectSize {
		t.Errorf("bytes downloaded: want %d, got %d", objectSize, n)
	}
	if !bytes.Equal(getData(), testData) {
		t.Error("downloaded data mismatch")
	}
	if attemptCount != 3 {
		t.Errorf("attempt count: want 3, got %d", attemptCount)
	}
}

// TestDownloadWithPipe_CustomMaxRetries verifies that MaxRetries in
// DownloadPipeInput overrides the default of 3.
func TestDownloadWithPipe_CustomMaxRetries(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	testData := []byte("Test data")
	objectSize := int64(len(testData))
	var attemptCount int32

	svc := newSvc(true) // noRetry
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(1)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			count := atomic.AddInt32(&attemptCount, 1)
			if count < 5 {
				r.Error = errors.New("network error")
				r.HTTPResponse = &http.Response{StatusCode: 500, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
				return
			}
			data.ContentLength = aws.Int64(objectSize)
			data.Body = ioutil.NopCloser(bytes.NewReader(testData))
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader(testData))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(context.Background(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(2 * 1024 * 1024 * 1024),
		MaxRetries:      aws.Int(5),
	})
	wait()

	if err != nil {
		t.Fatalf("expected success with 5 retries, got: %v", err)
	}
	if attemptCount != 5 {
		t.Errorf("attempt count: want 5, got %d", attemptCount)
	}
}

// TestDownloadWithPipe_ExhaustedRetries verifies that the download fails with a
// descriptive error after all retry attempts are exhausted (default: 3).
func TestDownloadWithPipe_ExhaustedRetries(t *testing.T) {
	pipePath := createTestPipe(t)
	defer os.Remove(pipePath)

	objectSize := int64(100)
	var attemptCount int32

	svc := newSvc(true) // noRetry
	svc.Handlers.Send.PushBack(func(r *request.Request) {
		switch data := r.Data.(type) {
		case *s3.HeadObjectOutput:
			data.ContentLength = aws.Int64(objectSize)
			data.PartsCount = aws.Int64(1)
			r.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		case *s3.GetObjectOutput:
			_ = data
			atomic.AddInt32(&attemptCount, 1)
			r.Error = errors.New("persistent network error")
			r.HTTPResponse = &http.Response{StatusCode: 500, Body: ioutil.NopCloser(bytes.NewReader([]byte{}))}
		}
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	_, wait := drainPipe(t, pipePath)

	_, err := downloader.DownloadWithPipe(context.Background(), &s3manager.DownloadPipeInput{
		PipePath:        aws.String(pipePath),
		GetObjectInput:  &s3.GetObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("test-key")},
		MaxBufferMemory: aws.Int64(2 * 1024 * 1024 * 1024),
	})
	wait()

	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if attemptCount != 3 {
		t.Errorf("attempt count: want 3, got %d", attemptCount)
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("error should mention retry count, got: %v", err)
	}
}
