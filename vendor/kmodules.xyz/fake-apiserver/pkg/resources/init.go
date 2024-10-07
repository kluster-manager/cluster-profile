/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resources

import (
	"context"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func InitCluster(cfg *rest.Config) error {
	kc := kubernetes.NewForConfigOrDie(cfg)
	err := CreateNamespace(kc, metav1.NamespaceDefault)
	if err != nil {
		return err
	}
	err = CreateNamespace(kc, metav1.NamespaceSystem)
	if err != nil {
		return err
	}
	err = CreateExtensionApiserverAuthentication(kc)
	if err != nil {
		return err
	}
	return nil
}

func CreateNamespace(kc *kubernetes.Clientset, ns string) error {
	obj := &core.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	_, err := kc.CoreV1().Namespaces().Create(context.TODO(), obj, metav1.CreateOptions{})
	return err
}

func CreateExtensionApiserverAuthentication(kc *kubernetes.Clientset) error {
	obj := &core.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "extension-apiserver-authentication",
			Namespace: metav1.NamespaceSystem,
		},
		Data: map[string]string{
			"client-ca-file": `-----BEGIN CERTIFICATE-----
MIIC/jCCAeagAwIBAgIBADANBgkqhkiG9w0BAQsFADAVMRMwEQYDVQQDEwprdWJl
cm5ldGVzMB4XDTIzMDkxMDE1Mjc0NFoXDTMzMDkwNzE1Mjc0NFowFTETMBEGA1UE
AxMKa3ViZXJuZXRlczCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAMKZ
VNVW5viA51KDdS/nZBcr3g5ypvvwJ3jwJkWIBq1txsTGpRheZ37MPPjnd7QwGQfZ
iBtsC5K7EJjNg3mBmziyPbyUzPYkBDv7dnw629Jb7Y4dcMdLS+QoZ3HTtgO9LgN3
DpWCuCcW76UrnHJLuG1Ml9DxjENA3RYJJM2J/HqRmflZCtpXtAwK+af4HMgRVhUe
DjyKZMypVxtgbsOr/4UYZfyCjptGXZlcJzzG+Lia3fq8V+yefjEJ6y9Y3VVZ87t7
E6459EzZ1l0kX0uZlsDkiiUNSkFOSqs+9XmSyONIRxPlZDIWBRJ27OyWWjyVQXfT
5+bUJFAYKHmR8pcJFckCAwEAAaNZMFcwDgYDVR0PAQH/BAQDAgKkMA8GA1UdEwEB
/wQFMAMBAf8wHQYDVR0OBBYEFBpn+vlD+PYQz9wi0iQ2H4LT1wzmMBUGA1UdEQQO
MAyCCmt1YmVybmV0ZXMwDQYJKoZIhvcNAQELBQADggEBADoySpRuoBBjjeBlo68O
HGuB9E5NnLv13Z9DAtw0bDi523oIQBryoNpuIzeztRlzOrk9iMGASVyyHNsA8+G/
KinO7S5WV32O83ni/18rcjGEX1VAKX67v3o7bVNJxo3ulkRBTFdRCwQYekDY6dZd
Dl+kh1yjCYqhHdMRbwbZiD/Wd5tC/3ctVfG3rFPKM3v5fTGWrnwom9IEEpCnBjN4
EcBLEGd5adwvMO9SoGR3YKIm4xEme4B52O7R3vB1z8bovGl4n5GaQLiIB93OYuPT
Y3DP0i6XwxyOZohv3Nrx2Ibpi5Vl5RtCopWZmkEcjNd8hw41/eXQcpSSuvY7WIvR
Exo=
-----END CERTIFICATE-----`,
			"requestheader-allowed-names": `["front-proxy-client"]`,
			"requestheader-client-ca-file": `-----BEGIN CERTIFICATE-----
MIIDCjCCAfKgAwIBAgIBADANBgkqhkiG9w0BAQsFADAZMRcwFQYDVQQDEw5mcm9u
dC1wcm94eS1jYTAeFw0yMzA5MTAxNTI3NDVaFw0zMzA5MDcxNTI3NDVaMBkxFzAV
BgNVBAMTDmZyb250LXByb3h5LWNhMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIB
CgKCAQEAtrTXbcChRs7QSxdsPvSgMvXUXT//Dncs0zJUBDsMYWAbEOBStRqp6Spv
RlTPLOrOBi86RXyqtnw+0/QT62Is1QH2s6mAy2u0BsCOkZ4NimfPRx73sIcxwwe6
8Ryus+jmojuLqPZ5/1W562fyHoLtwhxqLJHMl9iSpBLpB/qJjKMWgGS0BOoyE0RN
OvbNwqOMawaM4MYVs+QOZX4AIdqA9ydeOjCkA/r30pGz0ZM93e4KHjBfs5G3xbEV
AGsmuY8feovvyYKMs98VdmJdvjgXxo3nDd2cUNxTZvvIgoiWMQmezpJf/grP6h6u
ifyzGwBX8BiRXT9WNIba6qtKR11BIwIDAQABo10wWzAOBgNVHQ8BAf8EBAMCAqQw
DwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4EFgQUKcXifJRam5XO2TjH2W8MKV+ZWTIw
GQYDVR0RBBIwEIIOZnJvbnQtcHJveHktY2EwDQYJKoZIhvcNAQELBQADggEBALXV
o3P76CgPs7Uw5/YmPqwfPmqu4aK46oYX1CqZV7shwhCU4oa+StrCQXyLizjtHKKM
ALatYfECdxXpJwQd4J9NKsZiwuzSqvKiAkS0hMWwSjtd3Ym64JPWgTWD/DSYPmRM
4TFOY2JLB4LhA4dNBg45TgY3StCXPHHOIn8rxxQE4vfzuf12mNuwLeAmXjZ5nwoT
iJveTY8LwmB7sGvoDkBaDADq4LeWdLfNLG2aa/ljFYEGYE+HjLlr4g8bC2vLH51T
AIXt3Y5bbjGvwRvFqd44uJFqVzJC9H4AKaHOuv2bn06pLDCNbT8fHNKC4hMo8R02
bH5seJWwLKW0V0+smsY=
-----END CERTIFICATE-----`,
			"requestheader-extra-headers-prefix": `["X-Remote-Extra-"]`,
			"requestheader-group-headers":        `["X-Remote-Group"]`,
			"requestheader-username-headers":     `["X-Remote-User"]`,
		},
	}
	_, err := kc.CoreV1().ConfigMaps(metav1.NamespaceSystem).Create(context.TODO(), obj, metav1.CreateOptions{})
	return err
}
