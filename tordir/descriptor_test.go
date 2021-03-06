package tordir

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/mmcloughlin/openssl"
	"github.com/mmcloughlin/pearl/torexitpolicy"
	"github.com/mmcloughlin/pearl/torkeys"
	"github.com/mmcloughlin/pearl/torkeys/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var keyPEM = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQDAoNQlDZK5WhkHSJJ2qZShvINGrtywyfsMC9aeiyxehmtqoMsl
t1WlbMoA9wR84rDdb7D+DW6Z1UrBPgwlbm27D3mVOQ+brnilyE5+KbIjg1K5e6m4
6MKTOzs7G1nM4A70dd0zBPwHSYBwP0S9JiFRVqtKG36NynSpYZKissIMywIDAQAB
AoGAEvbCa/NuInlQRXtLkAsZ6uJYOjk02OLJqGHx+yHQeG3bXV//H/NwpxySto2b
D4Bx0RsR3bEM1nA9L9Ef+P9qJfieLrFRQ0KWFov7ZAh6sDJpFyojifu1jfo+hvqy
g76ku/798wb7fxtU+bsPyXMOyQdKaKw4miEwX7D2rahO6gECQQDkYbF2Hk0x0gK+
HLuN3fb/5/303XgskQ2qMER/Vwe7+WysgsPSfW6HqL+Sqh6bD6mJUpBC2DXbk0TC
5obzGT1BAkEA1+xFjAfSEFtc92PA3jhzuxK+kpgIQ5eBcrWHTgZS0s4qKICafP0B
jXb+SD0eWdwCBqnUFn8MeX57Qyk6GkKrCwJAOcxnpycgDj3CJ+8JoGvOeRFzeica
pNzJAotYqomSEYacdERb3seT041nfmzDdibOl0xn6iLh7oIk4taIzLlUgQJBAK8l
6BA7s8ky40mFsEhSEIaaIN421tVFS2rqF1RSStLXC1mJYEesz5qaAJBGi50mmroe
/nw1GMBgucnz4j60/5sCQBsQm1M1Hf+wYeIXrY0punjjTfFV6gD8gA1GT5XgP0aF
0MmPBrRS47B0WRDTNhQUtjtOZFAWbo5BEUQRukJwyIM=
-----END RSA PRIVATE KEY-----
`)

func BuildValidServerDescriptorWithKey(k torkeys.PrivateKey) *ServerDescriptor {
	s := NewServerDescriptor()
	s.SetRouter("nickname", net.IPv4(1, 2, 3, 4), 9001, 0)
	s.SetBandwidth(1000, 2000, 500)
	s.SetPublishedTime(time.Unix(0, 0))
	s.SetExitPolicy(torexitpolicy.RejectAllPolicy)
	s.SetOnionKey(k)
	s.SetSigningKey(k)
	return s
}

func BuildValidServerDescriptor() *ServerDescriptor {
	k, err := openssl.LoadPrivateKeyFromPEM(keyPEM)
	if err != nil {
		panic(err)
	}
	return BuildValidServerDescriptorWithKey(k)
}

func TestBuildValidServerDescriptor(t *testing.T) {
	d := BuildValidServerDescriptor()
	assert.NoError(t, d.Validate())
}

func TestServerDescriptor(t *testing.T) {
	s := NewServerDescriptor()

	k, err := openssl.LoadPrivateKeyFromPEM(keyPEM)
	require.NoError(t, err)

	// router (required)
	assert.Error(t, s.Validate())
	s.SetRouter("nickname", net.IPv4(1, 2, 3, 4), 9001, 0)

	// bandwidth (required)
	assert.Error(t, s.Validate())
	s.SetBandwidth(1000, 2000, 500)

	// published time (required)
	assert.Error(t, s.Validate())
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	s.SetPublishedTime(time.Date(2016, 12, 25, 10, 33, 17, 3534, loc))

	// onion-key (required)
	assert.Error(t, s.Validate())
	s.SetOnionKey(k)

	// signing-key (required)
	assert.Error(t, s.Validate())
	s.SetSigningKey(k)

	// exit policy (required)
	assert.Error(t, s.Validate())
	s.SetExitPolicy(torexitpolicy.RejectAllPolicy)

	// should have all required fields
	assert.NoError(t, s.Validate())

	doc, err := s.Document()
	require.NoError(t, err)

	expect, err := ioutil.ReadFile("./testdata/descriptors/example")
	require.NoError(t, err)
	assert.Equal(t, expect, doc.Encode())
}

func TestServerDescriptorCreateInvalid(t *testing.T) {
	s := NewServerDescriptor()
	_, err := s.Document()
	assert.Error(t, err)
}

func TestServerDescriptorSetRouterErrors(t *testing.T) {
	s := NewServerDescriptor()

	err := s.SetRouter("^%*^%*^%*", net.IPv4(1, 2, 3, 4), 9001, 0)
	assert.Error(t, err)

	addr := net.ParseIP("2001:4860:0:2001::68")
	err = s.SetRouter("nickname", addr, 9001, 0)
	assert.Error(t, err)
}

func TestServerDescriptorSetKeysError(t *testing.T) {
	m := &mocks.PrivateKey{}
	m.On("MarshalPKCS1PublicKeyDER").Return(nil, assert.AnError).Times(3)

	s := NewServerDescriptor()

	err := s.SetOnionKey(m)
	assert.Error(t, err)

	err = s.SetSigningKey(m)
	assert.Error(t, err)

	err = s.setFingerprint(m)
	assert.Error(t, err)

	m.AssertExpectations(t)
}

func TestServerDescriptorSetFingerprintError(t *testing.T) {
	m := &mocks.PrivateKey{}
	m.On("MarshalPKCS1PublicKeyDER").Return([]byte("data"), nil).Once()
	m.On("MarshalPKCS1PublicKeyDER").Return(nil, assert.AnError).Once()

	s := NewServerDescriptor()
	err := s.SetSigningKey(m)
	assert.Error(t, err)

	m.AssertExpectations(t)
}

func TestServerDescriptorSignatureError(t *testing.T) {
	k := &mocks.PrivateKey{}
	k.On("MarshalPKCS1PublicKeyDER").Return([]byte("pem"), nil).Times(3)
	k.On("PrivateEncrypt", mock.AnythingOfType("[]uint8")).Return(nil, assert.AnError).Once()

	s := BuildValidServerDescriptorWithKey(k)

	_, err := s.Document()
	assert.Error(t, err)
	k.AssertExpectations(t)
}

func TestServerDescriptorMissingFieldError(t *testing.T) {
	err := ServerDescriptorMissingFieldError("foo")
	assert.EqualError(t, err, "missing field 'foo'")
}

func TestPublishPublic(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	for _, addr := range Authorities {
		httpmock.RegisterResponder(
			http.MethodPost,
			fmt.Sprintf("http://%s/tor/", addr),
			httpmock.NewBytesResponder(200, nil),
		)
	}

	d := BuildValidServerDescriptor()
	err := d.PublishPublic()
	assert.NoError(t, err)
}

func TestPublishInvalid(t *testing.T) {
	d := NewServerDescriptor()
	err := d.PublishPublic()
	assert.Error(t, err)
}

func TestPublishHTTPErrors(t *testing.T) {
	statusCodes := []int{303, 400, 404, 503}
	for _, statusCode := range statusCodes {
		t.Run(fmt.Sprintf("status%d", statusCode), func(t *testing.T) {
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()

			addr := Authorities[rand.Intn(len(Authorities))]
			httpmock.RegisterResponder(
				http.MethodPost,
				fmt.Sprintf("http://%s/tor/", addr),
				httpmock.NewBytesResponder(statusCode, nil),
			)

			d := BuildValidServerDescriptor()
			err := d.PublishToAuthority(addr)
			assert.Error(t, err)
		})
	}
}
