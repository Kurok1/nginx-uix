/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto"
	"fmt"

	"golang.org/x/crypto/acme"
)

// ACMEOrder is the bounded issuance state required by the task runner.
type ACMEOrder struct {
	URI               string
	Status            string
	AuthorizationURLs []string
	FinalizeURL       string
}

// ACMEAuthorization contains only one order's validation choices.
type ACMEAuthorization struct {
	URI        string
	Status     string
	Identifier string
	Wildcard   bool
	Challenges []ACMEChallenge
}

// ACMEChallenge is a short-lived in-memory challenge handle.
type ACMEChallenge struct {
	Type   string
	URI    string
	Token  string
	Status string
}

// ACMEOrderClient is the narrow RFC 8555 issuance surface consumed by OrderService.
type ACMEOrderClient interface {
	CreateOrder(context.Context, []string) (ACMEOrder, error)
	Authorization(context.Context, string) (ACMEAuthorization, error)
	HTTP01Response(string) (string, error)
	DNS01Record(string) (string, error)
	Accept(context.Context, ACMEChallenge) error
	WaitAuthorization(context.Context, string) error
	Finalize(context.Context, string, []byte) ([][]byte, error)
}

// ACMEOrderClientFactory binds one stored account identity to an order client.
type ACMEOrderClientFactory interface {
	NewOrderClient(string, crypto.Signer, string) (ACMEOrderClient, error)
}

// NewOrderClient binds one supported key and exact account URI to a fixed directory.
func (factory *XCryptoACMEFactory) NewOrderClient(
	directoryURL string,
	key crypto.Signer,
	accountURI string,
) (ACMEOrderClient, error) {
	if factory == nil || factory.httpClient == nil || !validPrivateKey(key) ||
		!factory.allowsDirectory(directoryURL) ||
		!validACMEAccountURI(directoryURL, accountURI) {
		return nil, fmt.Errorf("create ACME order client: %w", ErrACMEAccountInvalid)
	}
	client := &acme.Client{
		Key: key, HTTPClient: factory.httpClient, DirectoryURL: directoryURL,
		KID: acme.KeyID(accountURI), UserAgent: "nginx-uix/0.5",
	}
	return &xCryptoACMEOrderClient{client: client}, nil
}

type xCryptoACMEOrderClient struct{ client *acme.Client }

func (client *xCryptoACMEOrderClient) CreateOrder(ctx context.Context, identifiers []string) (ACMEOrder, error) {
	values := make([]acme.AuthzID, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = acme.AuthzID{Type: "dns", Value: identifier}
	}
	order, err := client.client.AuthorizeOrder(ctx, values)
	if err != nil {
		return ACMEOrder{}, err
	}
	if order == nil {
		return ACMEOrder{}, ErrACMEUnavailable
	}
	return ACMEOrder{
		URI: order.URI, Status: order.Status,
		AuthorizationURLs: append([]string{}, order.AuthzURLs...), FinalizeURL: order.FinalizeURL,
	}, nil
}

func (client *xCryptoACMEOrderClient) Authorization(ctx context.Context, uri string) (ACMEAuthorization, error) {
	authorization, err := client.client.GetAuthorization(ctx, uri)
	if err != nil {
		return ACMEAuthorization{}, err
	}
	if authorization == nil {
		return ACMEAuthorization{}, ErrACMEUnavailable
	}
	challenges := make([]ACMEChallenge, 0, len(authorization.Challenges))
	for _, challenge := range authorization.Challenges {
		if challenge == nil {
			continue
		}
		challenges = append(challenges, ACMEChallenge{
			Type: challenge.Type, URI: challenge.URI, Token: challenge.Token, Status: challenge.Status,
		})
	}
	return ACMEAuthorization{
		URI: authorization.URI, Status: authorization.Status,
		Identifier: authorization.Identifier.Value, Wildcard: authorization.Wildcard,
		Challenges: challenges,
	}, nil
}

func (client *xCryptoACMEOrderClient) HTTP01Response(token string) (string, error) {
	return client.client.HTTP01ChallengeResponse(token)
}

func (client *xCryptoACMEOrderClient) DNS01Record(token string) (string, error) {
	return client.client.DNS01ChallengeRecord(token)
}

func (client *xCryptoACMEOrderClient) Accept(ctx context.Context, challenge ACMEChallenge) error {
	_, err := client.client.Accept(ctx, &acme.Challenge{
		Type: challenge.Type, URI: challenge.URI, Token: challenge.Token, Status: challenge.Status,
	})
	return err
}

func (client *xCryptoACMEOrderClient) WaitAuthorization(ctx context.Context, uri string) error {
	authorization, err := client.client.WaitAuthorization(ctx, uri)
	if err != nil {
		return err
	}
	if authorization == nil || authorization.Status != acme.StatusValid {
		return ErrACMEUnavailable
	}
	return nil
}

func (client *xCryptoACMEOrderClient) Finalize(
	ctx context.Context,
	finalizeURL string,
	csrDER []byte,
) ([][]byte, error) {
	chain, _, err := client.client.CreateOrderCert(ctx, finalizeURL, csrDER, true)
	return chain, err
}
