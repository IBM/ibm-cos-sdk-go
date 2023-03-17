package ibmiam

import (
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam/token"
)

const TrustedProfileProviderName = "TrustedProfileProviderNameIBM"

// TrustedProfileProvider Struct
type TrustedProfileProvider struct {
	// Name of Provider
	providerName string

	// Type of Provider - SharedCred, SharedConfig, etc.
	providerType string

	// Authenticator implements an IAM-based authentication schema
	authenticator *core.ContainerAuthenticator

	// Error
	ErrorStatus error

	// Logger attributes
	logger   aws.Logger
	logLevel *aws.LogLevelType
}

// NewTrustedProfileProvider allows the creation of a custom IBM IAM Provider
// Parameters:
//
//	Provider Name
//	AWS Config
//	Trusted Profile Name
//	Compute Resource Token File Path
//	IBM IAM Authentication Server Endpoint
//	Service Instance ID
//
// Returns:
//
//	TrustedProfileProvider
func NewTrustedProfileProvider(providerName string, config *aws.Config, trustedProfileName, crTokenFilePath,
	authEndPoint string) *TrustedProfileProvider {
	provider := new(TrustedProfileProvider)

	provider.providerName = providerName
	provider.providerType = "oauth"

	logLevel := aws.LogLevel(aws.LogOff)
	if config != nil && config.LogLevel != nil && config.Logger != nil {
		logLevel = config.LogLevel
		provider.logger = config.Logger
	}
	provider.logLevel = logLevel

	if crTokenFilePath == "" {
		provider.ErrorStatus = awserr.New("crTokenFilePathNotFound", "CR token file path not found", nil)
		if provider.logLevel.Matches(aws.LogDebug) {
			provider.logger.Log(debugLog, "<IBM IAM PROVIDER BUILD>", provider.ErrorStatus)
		}

		return provider
	}

	if trustedProfileName == "" {
		provider.ErrorStatus = awserr.New("trustedProfileNameNotFound", "Trusted Profile name not found", nil)
		if provider.logLevel.Matches(aws.LogDebug) {
			provider.logger.Log(debugLog, "<IBM IAM PROVIDER BUILD>", provider.ErrorStatus)
		}

		return provider
	}

	if authEndPoint == "" {
		authEndPoint = defaultAuthEndPoint
		if provider.logLevel.Matches(aws.LogDebug) {
			provider.logger.Log(debugLog, "<IBM IAM PROVIDER BUILD>", "using default auth endpoint", authEndPoint)
		}
	}

	authenticator, err := core.NewContainerAuthenticatorBuilder().
		SetIAMProfileName(trustedProfileName).
		SetCRTokenFilename(crTokenFilePath).
		SetURL(authEndPoint).
		Build()
	if err != nil {
		provider.ErrorStatus = awserr.New("errCreatingAuthenticatorClient", "cannot setup new Authenticator client", err)
		if provider.logLevel.Matches(aws.LogDebug) {
			provider.logger.Log(debugLog, "<IBM IAM PROVIDER BUILD>", provider.ErrorStatus)
		}

		return provider
	}

	provider.authenticator = authenticator

	return provider
}

// IsValid ...
// Returns:
//
//	Provider validation - boolean
func (p *TrustedProfileProvider) IsValid() bool {
	return nil == p.ErrorStatus
}

// Retrieve ...
// Returns:
//
//	Credential values
//	Error
func (p *TrustedProfileProvider) Retrieve() (credentials.Value, error) {
	if p.ErrorStatus != nil {
		if p.logLevel.Matches(aws.LogDebug) {
			p.logger.Log(debugLog, ibmiamProviderLog, p.providerName, p.ErrorStatus)
		}
		return credentials.Value{ProviderName: p.providerName}, p.ErrorStatus
	}

	tokenValue, err := p.authenticator.RequestToken()
	if err != nil {
		var returnErr error
		if p.logLevel.Matches(aws.LogDebug) {
			p.logger.Log(debugLog, ibmiamProviderLog, p.providerName, "ERROR ON GET token", err)
			returnErr = awserr.New("TokenManagerRetrieveError", "error retrieving the token", err)
		} else {
			returnErr = awserr.New("TokenManagerRetrieveError", "error retrieving the token", nil)
		}
		return credentials.Value{}, returnErr
	}
	if p.logLevel.Matches(aws.LogDebug) {
		p.logger.Log(debugLog, ibmiamProviderLog, p.providerName, "GET TOKEN", tokenValue)
	}

	token := token.Token{
		AccessToken:  tokenValue.AccessToken,
		RefreshToken: tokenValue.RefreshToken,
		TokenType:    tokenValue.TokenType,
		ExpiresIn:    tokenValue.ExpiresIn,
		Expiration:   tokenValue.Expiration,
	}

	return credentials.Value{Token: token, ProviderName: p.providerName, ProviderType: p.providerType}, nil
}

// IsExpired ...
//
//	TrustedProfileProvider expired or not - boolean
func (p *TrustedProfileProvider) IsExpired() bool {
	return true
}

// NewTPProvider constructor of the IBM IAM provider that uses trusted profile and CR token passed directly
// Returns: NewTrustedProfileProvider (AWS type)
func NewTPProvider(config *aws.Config, authEndPoint, trusterProfileName, crTokenFilePath string) *TrustedProfileProvider {
	return NewTrustedProfileProvider(TrustedProfileProviderName, config, trusterProfileName, crTokenFilePath, authEndPoint)
}

// NewTrustedProfileCredentials constructor for IBM IAM that uses IAM credentials passed in
// Returns: credentials.NewCredentials(NewTPProvider()) (AWS type)
func NewTrustedProfileCredentials(config *aws.Config, authEndPoint, trusterProfileName, crTokenFilePath string) *credentials.Credentials {
	return credentials.NewCredentials(NewTPProvider(config, authEndPoint, trusterProfileName, crTokenFilePath))
}
