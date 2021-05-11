package s3_test

import (
	"crypto/md5"
	"encoding/base64"
	"io/ioutil"
	"testing"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/request"
	"github.com/IBM/ibm-cos-sdk-go/awstesting/unit"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

func assertMD5(t *testing.T, req *request.Request) {
	err := req.Build()
	if err != nil {
		t.Errorf("expected no error, but received %v", err)
	}

	b, _ := ioutil.ReadAll(req.HTTPRequest.Body)
	out := md5.Sum(b)
	if len(b) == 0 {
		t.Error("expected non-empty value")
	}
	if a := req.HTTPRequest.Header.Get("Content-MD5"); len(a) == 0 {
		t.Fatal("Expected Content-MD5 header to be present in the operation request, was not")
	} else if e := base64.StdEncoding.EncodeToString(out[:]); e != a {
		t.Errorf("expected %s, but received %s", e, a)
	}
}

// IBM COS S3 Extension Test (required when retention is enabled)
func TestMD5InCompleteMultipartUpload(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.CompleteMultipartUploadRequest(&s3.CompleteMultipartUploadInput{
		Bucket: aws.String("bucketname"),
		Key:    aws.String("key"),
		MultipartUpload: &s3.CompletedMultipartUpload{
			Parts: []*s3.CompletedPart{{
				ETag:       aws.String("ETag"),
				PartNumber: aws.Int64(int64(1))}},
		},
		UploadId: aws.String("id"),
	})
	assertMD5(t, req)
}

func TestMD5InPutBucketCors(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutBucketCorsRequest(&s3.PutBucketCorsInput{
		Bucket: aws.String("bucketname"),
		CORSConfiguration: &s3.CORSConfiguration{
			CORSRules: []*s3.CORSRule{
				{
					AllowedMethods: []*string{aws.String("GET")},
					AllowedOrigins: []*string{aws.String("*")},
				},
			},
		},
	})
	assertMD5(t, req)
}

func TestMD5InDeleteObjects(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.DeleteObjectsRequest(&s3.DeleteObjectsInput{
		Bucket: aws.String("bucketname"),
		Delete: &s3.Delete{
			Objects: []*s3.ObjectIdentifier{
				{Key: aws.String("key")},
			},
		},
	})
	assertMD5(t, req)
}

func TestMD5InPutBucketLifecycleConfiguration(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutBucketLifecycleConfigurationRequest(&s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String("bucketname"),
		// IBM uses the LifecycleConfiguration object and permits only one rule
		LifecycleConfiguration: &s3.LifecycleConfiguration{
			Rules: []*s3.LifecycleRule{
				{Filter: &s3.LifecycleRuleFilter{Prefix: nil}, Status: aws.String(s3.ExpirationStatusEnabled)},
			},
		},
	})
	assertMD5(t, req)
}

func TestMD5InPutBucketAcl(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutBucketAclRequest(&s3.PutBucketAclInput{
		Bucket: aws.String("bucketname"),
		AccessControlPolicy: &s3.AccessControlPolicy{
			Grants: []*s3.Grant{{
				Grantee: &s3.Grantee{
					ID:   aws.String("mock id"),
					Type: aws.String("type"),
				},
				Permission: aws.String(s3.PermissionFullControl),
			}},
			Owner: &s3.Owner{
				DisplayName: aws.String("mock name"),
			},
		},
	})
	assertMD5(t, req)
}

// IBM COS S3 Extension Test
func TestMD5InPutBucketProtectionConfiguration(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutBucketProtectionConfigurationRequest(&s3.PutBucketProtectionConfigurationInput{
		Bucket: aws.String("bucket name"),
		ProtectionConfiguration: &s3.ProtectionConfiguration{
			Status: aws.String("Retention"),
			MinimumRetention: &s3.BucketProtectionMinimumRetention{
				Days: aws.Int64(10),
			},
			DefaultRetention: &s3.BucketProtectionDefaultRetention{
				Days: aws.Int64(100),
			},
			MaximumRetention: &s3.BucketProtectionMaximumRetention{
				Days: aws.Int64(1000),
			},
		},
	})
	assertMD5(t, req)
}

func TestMD5InPutBucketWebsite(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutBucketWebsiteRequest(&s3.PutBucketWebsiteInput{
		Bucket: aws.String("bucket name"),
		WebsiteConfiguration: &s3.WebsiteConfiguration{
			ErrorDocument: &s3.ErrorDocument{
				Key: aws.String("error"),
			},
		},
	})

	assertMD5(t, req)
}

func TestMD5InPutObjectAcl(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutObjectAclRequest(&s3.PutObjectAclInput{
		AccessControlPolicy: &s3.AccessControlPolicy{
			Grants: []*s3.Grant{{
				Grantee: &s3.Grantee{
					ID:   aws.String("mock id"),
					Type: aws.String("type"),
				},
				Permission: aws.String(s3.PermissionFullControl),
			}},
			Owner: &s3.Owner{
				DisplayName: aws.String("mock name"),
			},
		},
		Bucket: aws.String("bucket name"),
		Key:    aws.String("key"),
	})

	assertMD5(t, req)
}

func TestMD5InPutObjectTagging(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutObjectTaggingRequest(&s3.PutObjectTaggingInput{
		Bucket: aws.String("bucket name"),
		Key:    aws.String("key"),
		Tagging: &s3.Tagging{TagSet: []*s3.Tag{
			{
				Key:   aws.String("key"),
				Value: aws.String("value"),
			},
		}},
	})

	assertMD5(t, req)
}

func TestMD5InPutPublicAccessBlock(t *testing.T) {
	svc := s3.New(unit.Session)
	req, _ := svc.PutPublicAccessBlockRequest(&s3.PutPublicAccessBlockInput{
		Bucket: aws.String("bucket name"),
		PublicAccessBlockConfiguration: &s3.PublicAccessBlockConfiguration{
			BlockPublicAcls: aws.Bool(true),
			//BlockPublicPolicy:     aws.Bool(true),
			IgnorePublicAcls: aws.Bool(true),
			//RestrictPublicBuckets: aws.Bool(true),
		},
	})

	assertMD5(t, req)
}
