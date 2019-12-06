// +build go1.7

package endpoints

import (
	"regexp"
	"testing"
)

func TestEndpointFor_S3UsEast1RegionalFlag(t *testing.T) {

	// mock S3 regional endpoints model
	mockS3ModelPartition := partition{
		ID:        "aws",
		Name:      "AWS Standard",
		DNSSuffix: "amazonaws.com",
		RegionRegex: regionRegex{
			Regexp: func() *regexp.Regexp {
				reg, _ := regexp.Compile("^(us|eu|ap|sa|ca|me)\\-\\w+\\-\\d+$")
				return reg
			}(),
		},
		Defaults: endpoint{
			Hostname:          "{service}.{region}.{dnsSuffix}",
			Protocols:         []string{"https"},
			SignatureVersions: []string{"v4"},
		},
		Regions: regions{
			"ap-east-1": region{
				Description: "Asia Pacific (Hong Kong)",
			},
			"ap-northeast-1": region{
				Description: "Asia Pacific (Tokyo)",
			},
			"ap-northeast-2": region{
				Description: "Asia Pacific (Seoul)",
			},
			"ap-south-1": region{
				Description: "Asia Pacific (Mumbai)",
			},
			"ap-southeast-1": region{
				Description: "Asia Pacific (Singapore)",
			},
			"ap-southeast-2": region{
				Description: "Asia Pacific (Sydney)",
			},
			"ca-central-1": region{
				Description: "Canada (Central)",
			},
			"eu-central-1": region{
				Description: "EU (Frankfurt)",
			},
			"eu-north-1": region{
				Description: "EU (Stockholm)",
			},
			"eu-west-1": region{
				Description: "EU (Ireland)",
			},
			"eu-west-2": region{
				Description: "EU (London)",
			},
			"eu-west-3": region{
				Description: "EU (Paris)",
			},
			"me-south-1": region{
				Description: "Middle East (Bahrain)",
			},
			"sa-east-1": region{
				Description: "South America (Sao Paulo)",
			},
			"us-east-1": region{
				Description: "US East (N. Virginia)",
			},
			"us-east-2": region{
				Description: "US East (Ohio)",
			},
			"us-west-1": region{
				Description: "US West (N. California)",
			},
			"us-west-2": region{
				Description: "US West (Oregon)",
			},
		},
		Services: services{
			"s3": service{
				PartitionEndpoint: "aws-global",
				IsRegionalized:    boxedTrue,
				Defaults: endpoint{
					Protocols:         []string{"http", "https"},
					SignatureVersions: []string{"s3v4"},

					HasDualStack:      boxedTrue,
					DualStackHostname: "{service}.dualstack.{region}.{dnsSuffix}",
				},
				Endpoints: endpoints{
					"ap-east-1": endpoint{},
					"ap-northeast-1": endpoint{
						Hostname:          "s3.ap-northeast-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"ap-northeast-2": endpoint{},
					"ap-northeast-3": endpoint{},
					"ap-south-1":     endpoint{},
					"ap-southeast-1": endpoint{
						Hostname:          "s3.ap-southeast-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"ap-southeast-2": endpoint{
						Hostname:          "s3.ap-southeast-2.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"aws-global": endpoint{
						Hostname: "s3.amazonaws.com",
						CredentialScope: credentialScope{
							Region: "us-east-1",
						},
					},
					"ca-central-1": endpoint{},
					"eu-central-1": endpoint{},
					"eu-north-1":   endpoint{},
					"eu-west-1": endpoint{
						Hostname:          "s3.eu-west-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"eu-west-2":  endpoint{},
					"eu-west-3":  endpoint{},
					"me-south-1": endpoint{},
					"s3-external-1": endpoint{
						Hostname:          "s3-external-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
						CredentialScope: credentialScope{
							Region: "us-east-1",
						},
					},
					"sa-east-1": endpoint{
						Hostname:          "s3.sa-east-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"us-east-1": endpoint{
						Hostname:          "s3.us-east-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"us-east-2": endpoint{},
					"us-west-1": endpoint{
						Hostname:          "s3.us-west-1.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
					"us-west-2": endpoint{
						Hostname:          "s3.us-west-2.amazonaws.com",
						SignatureVersions: []string{"s3", "s3v4"},
					},
				},
			},
		},
	}

	// resolver for mock S3 regional endpoints model
	resolver := mockS3ModelPartition

	cases := map[string]struct {
		service, region     string
		regional            S3UsEast1RegionalEndpoint
		ExpectURL           string
		ExpectSigningRegion string
	}{
		// S3 Endpoints resolver tests:
		"s3/us-east-1/regional": {
			service:             "s3",
			region:              "us-east-1",
			regional:            RegionalS3UsEast1Endpoint,
			ExpectURL:           "https://s3.us-east-1.amazonaws.com",
			ExpectSigningRegion: "us-east-1",
		},
		"s3/us-east-1/legacy": {
			service:             "s3",
			region:              "us-east-1",
			ExpectURL:           "https://s3.amazonaws.com",
			ExpectSigningRegion: "us-east-1",
		},
		"s3/us-west-1/regional": {
			service:             "s3",
			region:              "us-west-1",
			regional:            RegionalS3UsEast1Endpoint,
			ExpectURL:           "https://s3.us-west-1.amazonaws.com",
			ExpectSigningRegion: "us-west-1",
		},
		"s3/us-west-1/legacy": {
			service:             "s3",
			region:              "us-west-1",
			regional:            RegionalS3UsEast1Endpoint,
			ExpectURL:           "https://s3.us-west-1.amazonaws.com",
			ExpectSigningRegion: "us-west-1",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var optionSlice []func(o *Options)
			optionSlice = append(optionSlice, func(o *Options) {
				o.S3UsEast1RegionalEndpoint = c.regional
			})

			actual, err := resolver.EndpointFor(c.service, c.region, optionSlice...)
			if err != nil {
				t.Fatalf("failed to resolve endpoint, %v", err)
			}

			if e, a := c.ExpectURL, actual.URL; e != a {
				t.Errorf("expect %v, got %v", e, a)
			}

			if e, a := c.ExpectSigningRegion, actual.SigningRegion; e != a {
				t.Errorf("expect %v, got %v", e, a)
			}

		})
	}
}
