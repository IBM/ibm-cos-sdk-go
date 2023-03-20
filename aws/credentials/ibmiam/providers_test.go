package ibmiam

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam/token"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam/tokenmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// Global IBM IAM Credential constants

	// API Key
	apikey = "ak"
	// Service Instance ID
	serviceinstanceid = "sii"
	// IBM IAM Authentication Server Endpoint
	authendpoint = "aep"
	// trustedProfileName
	trustedProfileName = "test-trusted-profile"
	// trustedProfileID
	trustedProfileID = "test-trusted-profile-id"
)

// Mock Token Manager
type tokenManagerMock struct {
	// Each TM has API Key
	apikey string

	// Each TM has IBM IAM Authentication Server Endpoint
	authendpoint string
}

// Mock Token Manager Using GET Function
// Returns:
//
//	Token object that has following
//	- Access Token
//	- Refresh Token
//	- Token Type
//	- Expires in (terms of seconds)
//	- Expiration time
//	Error object
func (tmm *tokenManagerMock) Get() (*token.Token, error) {
	return &token.Token{
		AccessToken:  "A",
		RefreshToken: "R",
		TokenType:    "T",
		ExpiresIn:    int64((time.Hour * 24).Seconds()),
		Expiration:   time.Now().Add(time.Hour * 24).Unix(),
	}, nil
}

// Mock Token Manager's Refresh()
func (tmm *tokenManagerMock) Refresh() error {
	return nil
}

// Mock Token Manager's StopBackgroundRefresh()
func (tmm *tokenManagerMock) StopBackgroundRefresh() {
	// A mock function
}

// Mock Token Manager's StartBackgroundRefresh()
func (tmm *tokenManagerMock) StartBackgroundRefresh() {
	// A mock function
}

// Mock Token Manager Constructor
// Parameters:
//
//	AWS Config
//	IBM IAM API Key
//	IBM IAM Authentication Server Endpoint
//	Advisory Refresh Timeout
//	Manadatory Refresh Timeout
//	Timer
//	Token Manager Client Do Operation
//
// Returns:
//
//	Mock Token Manager with API KEY and IBM IAM Authentication Server Endpoint
func newTMMock(_ *aws.Config, apiKey string, authEndPoint string, _,
	_ func(time.Duration) time.Duration, _ func() time.Time,
	_ tokenmanager.IBMClientDo) tokenmanager.API {
	return &tokenManagerMock{
		apikey:       apiKey,
		authendpoint: authEndPoint,
	}
}

// Goals for each test:
// API Key is the same as TokenManager passes in in a provider
// IBM IAM Authentication Endpoint is the same as TokenManager passes in in a provider
// Type of Provider is the same as TokenManager passes in
// Service Instance ID is the same as TokenManager passes in in a provider

// Test Static Credentials Provider with IBM IAM API Key
func TestStaticApiKey(t *testing.T) {
	realNTM := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	prov := NewStaticProvider(&aws.Config{}, authendpoint, apikey, serviceinstanceid)
	tokenmanager.NewTokenManagerFromAPIKey = realNTM

	tk, _ := prov.Retrieve()

	assert.Equal(t, apikey, prov.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint, prov.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, tk.ProviderName, StaticProviderName, "e3")
	assert.Equal(t, tk.ServiceInstanceID, serviceinstanceid, "e4")
}

// Test Trusted Profile Authentication using cr token
func TestTrustedProfile(t *testing.T) {
	testToken := "test"

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := token.Token{
			AccessToken:  testToken,
			RefreshToken: "not-supported",
			TokenType:    tokenType,
			ExpiresIn:    int64((time.Hour * 24).Seconds()),
			Expiration:   time.Now().Add(time.Hour * 24).Unix(),
		}

		data, err := json.Marshal(token)
		require.NoError(t, err)

		w.WriteHeader(http.StatusAccepted)
		_, err = w.Write(data)
		require.NoError(t, err)
	}))

	file, err := ioutil.TempFile(os.TempDir(), "crtoken")
	require.NoError(t, err)
	fmt.Println(file.Name())
	defer os.Remove(file.Name())

	_, err = file.Write([]byte("test cr token"))
	require.NoError(t, err)
	defer file.Close()

	prov := NewTrustedProfileProvider(TrustedProfileProviderName, &aws.Config{}, trustedProfileName,
		trustedProfileID, file.Name(), authServer.URL)

	assert.Equal(t, trustedProfileName, prov.authenticator.IAMProfileName, "trusted profile name did not match")
	assert.Equal(t, trustedProfileID, prov.authenticator.IAMProfileID, "trusted profile ID did not match")
	assert.Equal(t, authServer.URL, prov.authenticator.URL, "auth endpoint did not match")
	assert.Equal(t, file.Name(), prov.authenticator.CRTokenFilename, "cr token filepath did not match")
	assert.Equal(t, TrustedProfileProviderName, prov.providerName, "provider name did not match")

	cred, err := prov.Retrieve()
	require.NoError(t, err)

	assert.Equal(t, testToken, cred.AccessToken)
	assert.Equal(t, tokenType, cred.TokenType)
	assert.Equal(t, TrustedProfileProviderName, prov.providerName)
	assert.Equal(t, "oauth", prov.providerType)
}

// Test Environment Variable Provider with IBM IAM API Key
func TestEnvApiKey(t *testing.T) {
	os.Setenv("IBM_API_KEY_ID", apikey)
	os.Setenv("IBM_SERVICE_INSTANCE_ID", serviceinstanceid)
	os.Setenv("IBM_AUTH_ENDPOINT", authendpoint)
	realNTM := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	prov := NewEnvProvider(&aws.Config{})
	tokenmanager.NewTokenManagerFromAPIKey = realNTM

	tk, _ := prov.Retrieve()

	assert.Equal(t, apikey, prov.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint, prov.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, tk.ProviderName, EnvProviderName, "e3")
	assert.Equal(t, tk.ServiceInstanceID, serviceinstanceid, "e4")
}

// Create an INI variable with IBM IAM credentials with three profiles:
//
//	-Default with IBM IAM credentials
//	-Shared Credentials with IBM IAM credentials
//	-Shared Config with IBM IAM credentials
//
// Each one has API Key, Service Instance ID, IBM IAM Authentication Endpoint
var iniContent = `
[default]
ibm_api_key_id=%[1]s
ibm_service_instance_id=%[2]s
ibm_auth_endpoint=%[3]s

[shcred1]
ibm_api_key_id=%[1]sCRED
ibm_service_instance_id=%[2]sCRED
ibm_auth_endpoint=%[3]sCRED

[profile shconf1]
ibm_api_key_id=%[1]sCONF
ibm_service_instance_id=%[2]sCONF
ibm_auth_endpoint=%[3]sCONF
`

// Test Shared Credentials using IBM IAM API Key
func TestSharedCredentialsApiKey(t *testing.T) {

	// See TestSharedCredProfileApiKey for further
	// details of what test accomplishes
	f, e := ioutil.TempFile("", "")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(f.Name())

	f.WriteString(fmt.Sprintf(iniContent, apikey, serviceinstanceid, authendpoint))
	name := f.Name()
	f.Close()

	// Real new token manager then mock it up with the provider
	realNTM := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	prov := NewSharedCredentialsProvider(&aws.Config{}, name, "")
	tokenmanager.NewTokenManagerFromAPIKey = realNTM

	tk, _ := prov.Retrieve()

	assert.Equal(t, apikey, prov.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint, prov.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, tk.ProviderName, SharedCredsProviderName, "e3")
	assert.Equal(t, tk.ServiceInstanceID, serviceinstanceid, "e4")
}

// Test Shared Configuration Credential with IBM IAM API Key
func TestSharedConfigurationApiKey(t *testing.T) {

	f, e := ioutil.TempFile("", "")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(f.Name())

	f.WriteString(fmt.Sprintf(iniContent, apikey, serviceinstanceid, authendpoint))
	name := f.Name()
	f.Close()

	realNTM := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	prov := NewSharedConfigProvider(&aws.Config{}, name, "")
	tokenmanager.NewTokenManagerFromAPIKey = realNTM

	tk, _ := prov.Retrieve()

	assert.Equal(t, apikey, prov.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint, prov.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, tk.ProviderName, SharedConfProviderName, "e3")
	assert.Equal(t, tk.ServiceInstanceID, serviceinstanceid, "e4")
}

// Test Shared Configuration using Profile Credential with IBM IAM API Key
func TestSharedConfProfileApiKey(t *testing.T) {

	f, e := ioutil.TempFile("", "")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(f.Name())

	f.WriteString(fmt.Sprintf(iniContent, apikey, serviceinstanceid, authendpoint))
	name := f.Name()
	f.Close()

	realNTM := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	prov := NewSharedConfigProvider(&aws.Config{}, name, "shconf1")
	tokenmanager.NewTokenManagerFromAPIKey = realNTM

	tk, err := prov.Retrieve()

	assert.Nil(t, err)

	assert.Equal(t, apikey+"CONF", prov.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint+"CONF", prov.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, SharedConfProviderName, tk.ProviderName, "e3")
	assert.Equal(t, serviceinstanceid+"CONF", tk.ServiceInstanceID, "e4")
}

// Test Shared Credentials using Profile Credential with IBM IAM API Key
func TestSharedCredProfileApiKey(t *testing.T) {

	// Create a new buffer for a temporary file
	f, e := ioutil.TempFile("", "")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(f.Name())

	// Write String into a temp file with the following
	// - ini content
	// - API Key
	// - Service Instance ID
	// - IBM IAM Authentication Endpoint
	f.WriteString(fmt.Sprintf(iniContent, apikey, serviceinstanceid, authendpoint))
	name := f.Name()
	f.Close()

	// Setting New Token Manager with API Key
	realNewTokenManager := tokenmanager.NewTokenManagerFromAPIKey
	tokenmanager.NewTokenManagerFromAPIKey = newTMMock
	provider := NewSharedCredentialsProvider(&aws.Config{}, name, "shcred1")
	tokenmanager.NewTokenManagerFromAPIKey = realNewTokenManager

	// Provider to retrieve credentials based on the ini content passed in the temp file
	tk, _ := provider.Retrieve()

	// Verification for each value of credentials
	assert.Equal(t, apikey+"CRED", provider.tokenManager.(*tokenManagerMock).apikey, "e1")
	assert.Equal(t, authendpoint+"CRED", provider.tokenManager.(*tokenManagerMock).authendpoint, "e2")
	assert.Equal(t, SharedCredsProviderName, tk.ProviderName, "e3")
	assert.Equal(t, serviceinstanceid+"CRED", tk.ServiceInstanceID, "e4")
}

// Mock Token Manager Two
// Uses the first mock of token manager
type tokenManagerMock2 struct {
	tokenManagerMock
	init func() (*token.Token, error)
}

// Mock Token Manager Two GET function
func (tmm *tokenManagerMock2) Get() (*token.Token, error) {
	return tmm.init()
}

// Mock Token Manager Two Constructor
// Returns:
//
//	Mock Token Manager with IBM IAM Authentication Server Endpoint
func newTMMock2(_ *aws.Config, init func() (*token.Token, error), authEndPoint string, _,
	_ func(time.Duration) time.Duration, _ func() time.Time,
	_ tokenmanager.IBMClientDo) tokenmanager.API {
	return &tokenManagerMock2{
		tokenManagerMock: tokenManagerMock{
			authendpoint: authEndPoint,
		},
		init: init,
	}
}

// Test Programmatical Token
func TestProgramaticalToken(t *testing.T) {
	iinitFunc := func() (*token.Token, error) {
		return &token.Token{
			AccessToken:  "initA",
			RefreshToken: "initR",
			TokenType:    "initT",
			ExpiresIn:    int64((time.Hour * 248).Seconds()) * -1,
			Expiration:   time.Now().Add(-1 * time.Hour).Unix(),
		}, nil
	}

	realNTM := tokenmanager.NewTokenManager
	tokenmanager.NewTokenManager = newTMMock2
	prov := NewCustomInitFuncProvider(&aws.Config{}, iinitFunc, authendpoint, serviceinstanceid, nil)
	tokenmanager.NewTokenManager = realNTM

	tk, _ := prov.Retrieve()

	isExp := prov.IsExpired()

	// Check if the token is expired
	assert.Equal(t, authendpoint, prov.tokenManager.(*tokenManagerMock2).authendpoint, "e1")
	assert.True(t, isExp, "e2")
	assert.Equal(t, CustomInitFuncProviderName, tk.ProviderName, "e3")
	assert.Equal(t, serviceinstanceid, tk.ServiceInstanceID, "e4")
}