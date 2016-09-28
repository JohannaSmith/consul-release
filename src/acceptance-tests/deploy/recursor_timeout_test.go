package deploy_test

import (
	"fmt"
	"time"

	"github.com/cloudfoundry-incubator/consul-release/src/acceptance-tests/testing/helpers"
	"github.com/pivotal-cf-experimental/bosh-test/bosh"
	"github.com/pivotal-cf-experimental/destiny/consul"

	testconsumerclient "github.com/cloudfoundry-incubator/consul-release/src/acceptance-tests/testing/testconsumer/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	DELAY   = 10 * time.Second
	TIMEOUT = 30 * time.Second
)

var _ = Describe("recursor timeout", func() {
	var (
		consulManifest  consul.Manifest
		delayIncidentID string
		tcClient        testconsumerclient.Client
	)

	BeforeEach(func() {
		var err error
		config.TurbulenceHost = turbulenceManifest.Jobs[0].Networks[0].StaticIPs[0]

		consulManifest, _, err = helpers.DeployConsulWithTurbulence("dns-timeout", 1, boshClient, config)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() ([]bosh.VM, error) {
			return helpers.DeploymentVMs(boshClient, consulManifest.Name)
		}, "1m", "10s").Should(ConsistOf(helpers.GetVMsFromManifest(consulManifest)))

		tcClient = testconsumerclient.New(fmt.Sprintf("http://%s:6769", consulManifest.Jobs[1].Networks[0].StaticIPs[0]))
	})

	AfterEach(func() {
		if !CurrentGinkgoTestDescription().Failed {
			Eventually(func() string {
				incidentResp, err := turbulenceClient.Incident(delayIncidentID)
				Expect(err).NotTo(HaveOccurred())

				return incidentResp.ExecutionCompletedAt
			}, TIMEOUT.String(), "10s").ShouldNot(BeEmpty())
			err := boshClient.DeleteDeployment(consulManifest.Name)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("resolves long running DNS queries utilizing the consul recursor_timeout property", func() {
		By("making sure my-fake-server resolves", func() {
			address, err := tcClient.DNS("my-fake-server.fake.local")
			Expect(err).NotTo(HaveOccurred())

			// miekg/dns implementation responds with A and AAAA records regardless of the type of record requested
			// therefore we're expected 4 IPs here
			Expect(address).To(Equal([]string{"10.2.3.4", "10.2.3.4", "10.2.3.4", "10.2.3.4"}))
		})

		By("delaying DNS queries with a network delay that is greater than the recursor timeout", func() {
			response, err := turbulenceClient.Delay(consulManifest.Name, "fake-dns-server", []int{0}, DELAY, TIMEOUT)
			Expect(err).NotTo(HaveOccurred())
			delayIncidentID = response.ID
		})

		By("making a DNS query which should timeout", func() {
			address, err := tcClient.DNS("my-fake-server.fake.local")
			Expect(err).NotTo(HaveOccurred())
			Expect(address).To(BeEmpty())
		})

		By("waiting for the network delay to end", func() {
			Eventually(func() string {
				incidentResp, err := turbulenceClient.Incident(delayIncidentID)
				Expect(err).NotTo(HaveOccurred())

				return incidentResp.ExecutionCompletedAt
			}, TIMEOUT.String(), "10s").ShouldNot(BeEmpty())
		})

		By("redeploying with 30s recursor timeout", func() {
			consulManifest.Properties.Consul.Agent.DNSConfig.RecursorTimeout = "30s"

			yaml, err := consulManifest.ToYAML()
			Expect(err).NotTo(HaveOccurred())

			_, err = boshClient.Deploy(yaml)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() ([]bosh.VM, error) {
				return helpers.DeploymentVMs(boshClient, consulManifest.Name)
			}, "1m", "10s").Should(ConsistOf(helpers.GetVMsFromManifest(consulManifest)))
		})

		By("delaying DNS queries with a network delay that is less than the recursor timeout", func() {
			response, err := turbulenceClient.Delay(consulManifest.Name, "fake-dns-server", []int{0}, DELAY, TIMEOUT)
			Expect(err).NotTo(HaveOccurred())
			delayIncidentID = response.ID
		})

		By("successfully making a DNS query", func() {
			dnsStartTime := time.Now()
			address, err := tcClient.DNS("my-fake-server.fake.local")
			Expect(err).NotTo(HaveOccurred())

			dnsElapsedTime := time.Since(dnsStartTime)
			Expect(dnsElapsedTime.Nanoseconds()).To(BeNumerically(">", DELAY))

			// miekg/dns implementation responds with A and AAAA records regardless of the type of record requested
			// therefore we're expected 4 IPs here
			Expect(address).To(Equal([]string{"10.2.3.4", "10.2.3.4", "10.2.3.4", "10.2.3.4"}))
		})
	})
})