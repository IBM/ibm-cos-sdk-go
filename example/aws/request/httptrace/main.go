// build example

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"

	"os"
)

var clientCfg ClientConfig
var bucket string
var keyN = 0

func init() {
	clientCfg.SetupFlags("", flag.CommandLine)

	flag.CommandLine.StringVar(&bucket, "bucket", "",
		"The Bucket to send messages to")
}

func main() {
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		flag.CommandLine.PrintDefaults()
		exitErrorf(err, "failed to parse CLI commands")
	}
	if len(bucket) == 0 {
		flag.CommandLine.PrintDefaults()
		exitErrorf(errors.New("Bucket required"), "")
	}

	httpClient := NewClient(clientCfg)
	sess, err := session.NewSession(&aws.Config{
		HTTPClient: httpClient,

		// Disable Retries to prevent the httptrace's getting mixed up on
		// retries.
		MaxRetries: aws.Int(0),
	})
	if err != nil {
		exitErrorf(err, "failed to load config")
	}

	// Start making the requests.
	svc := s3.New(sess)
	ctx := context.Background()

	fmt.Printf("Message: ")

	// Scan messages from the input with newline separators.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		trace, err := publishMessage(ctx, svc, bucket, fmt.Sprintf("%06d", keyN), scanner.Text())
		keyN++
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to publish message, %v", err)
		}
		RecordTrace(os.Stdout, trace)

		fmt.Println()
		fmt.Printf("Message: ")
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to read input, %v", err)
	}
}

// publishMessage will send the message to the SNS topic returning an request
// trace for metrics.
func publishMessage(ctx context.Context, svc *s3.S3, bucket, key, msg string) (*RequestTrace, error) {
	traceCtx := NewRequestTrace(ctx)
	defer traceCtx.RequestDone()

	input := new(s3.PutObjectInput).SetBucket(bucket).SetKey(key).SetBody(strings.NewReader(msg))

	_, err := svc.PutObjectWithContext(traceCtx, input)
	if err != nil {
		return nil, err
	}

	return traceCtx, nil
}

func exitErrorf(err error, msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "FAILED: %v\n"+msg+"\n", append([]interface{}{err}, args...)...)
	os.Exit(1)
}
