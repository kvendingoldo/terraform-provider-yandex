package yandex

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"testing"

	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/mdb/redis/v1"
)

const redisResource = "yandex_mdb_redis_cluster.foo"
const redisResourceSharded = "yandex_mdb_redis_cluster.bar"

func init() {
	resource.AddTestSweepers("yandex_mdb_redis_cluster", &resource.Sweeper{
		Name: "yandex_mdb_redis_cluster",
		F:    testSweepMDBRedisCluster,
	})
}

func testSweepMDBRedisCluster(_ string) error {
	conf, err := configForSweepers()
	if err != nil {
		return fmt.Errorf("error getting client: %s", err)
	}

	resp, err := conf.sdk.MDB().Redis().Cluster().List(conf.Context(), &redis.ListClustersRequest{
		FolderId: conf.FolderID,
		PageSize: defaultMDBPageSize,
	})
	if err != nil {
		return fmt.Errorf("error getting Redis clusters: %s", err)
	}

	result := &multierror.Error{}
	for _, c := range resp.Clusters {
		if !sweepMDBRedisCluster(conf, c.Id) {
			result = multierror.Append(result, fmt.Errorf("failed to sweep Redis cluster %q", c.Id))
		}
	}

	return result.ErrorOrNil()
}

func sweepMDBRedisCluster(conf *Config, id string) bool {
	return sweepWithRetry(sweepMDBRedisClusterOnce, conf, "Redis cluster", id)
}

func sweepMDBRedisClusterOnce(conf *Config, id string) error {
	ctx, cancel := conf.ContextWithTimeout(yandexMDBRedisClusterDefaultTimeout)
	defer cancel()

	op, err := conf.sdk.MDB().Redis().Cluster().Delete(ctx, &redis.DeleteClusterRequest{
		ClusterId: id,
	})
	return handleSweepOperation(ctx, conf, op, err)
}

func mdbRedisClusterImportStep(name string) resource.TestStep {
	return resource.TestStep{
		ResourceName:      name,
		ImportState:       true,
		ImportStateVerify: true,
		ImportStateVerifyIgnore: []string{
			"config.0.password", // not returned
			"health",            // volatile value
			"host",              // the order of hosts differs
		},
	}
}

// Test that a Redis Cluster can be created, updated and destroyed
func TestAccMDBRedisCluster_full(t *testing.T) {
	t.Parallel()

	var r redis.Cluster
	redisName := acctest.RandomWithPrefix("tf-redis")
	redisDesc := "Redis Cluster Terraform Test"
	redisDesc2 := "Redis Cluster Terraform Test Updated"
	folderID := getExampleFolderID()
	version := "5.0"
	baseDiskSize := 16
	updatedDiskSize := 24
	diskTypeId := "network-ssd"
	baseFlavor := "hm1.nano"
	updatedFlavor := "hm1.micro"
	tlsEnabled := false

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckVPCNetworkDestroy,
		Steps: []resource.TestStep{
			// Create Redis Cluster
			{
				Config: testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRESTABLE", true, nil, version, baseFlavor, baseDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 1, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "ALLKEYS_LRU", 100,
						"Elg", 5000, 10, 15, version),
					testAccCheckMDBRedisClusterHasResources(&r, baseFlavor, baseDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "test_key", "test_value"),
					testAccCheckCreatedAtAttr(redisResource),
					resource.TestCheckResourceAttr(redisResource, "security_group_ids.#", "1"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.type", "WEEKLY"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.day", "FRI"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.hour", "20"),
					resource.TestCheckResourceAttr(redisResource, "deletion_protection", "true"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// uncheck 'deletion_protection'
			{
				Config: testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRESTABLE", false, nil, version, baseFlavor, baseDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 1, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "deletion_protection", "false"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// check 'deletion_protection'
			{
				Config: testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRESTABLE", true, nil, version, baseFlavor, baseDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 1, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "deletion_protection", "true"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// check 'deletion_protection
			{
				Config:      testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRODUCTION", true, nil, version, baseFlavor, baseDiskSize, ""),
				ExpectError: regexp.MustCompile(".*The operation was rejected because cluster has 'deletion_protection' = ON.*"),
			},
			// uncheck 'deletion_protection'
			{
				Config: testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRESTABLE", false, nil, version, baseFlavor, baseDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 1, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "deletion_protection", "false"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// Change some options
			{
				Config: testAccMDBRedisClusterConfigUpdated(redisName, redisDesc2, &tlsEnabled, version, updatedFlavor,
					updatedDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 1, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc2),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "VOLATILE_LFU", 200,
						"Ex", 6000, 12, 17, version),
					testAccCheckMDBRedisClusterHasResources(&r, updatedFlavor, updatedDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "new_key", "new_value"),
					testAccCheckCreatedAtAttr(redisResource),
					resource.TestCheckResourceAttr(redisResource, "security_group_ids.#", "2"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.type", "ANYTIME"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// Add new host
			{
				Config: testAccMDBRedisClusterConfigAddedHost(redisName, redisDesc2, nil, version, updatedFlavor,
					updatedDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 2, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc2),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					resource.TestCheckResourceAttrSet(redisResource, "host.1.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "VOLATILE_LFU", 200,
						"Ex", 6000, 12, 17, version),
					testAccCheckMDBRedisClusterHasResources(&r, updatedFlavor, updatedDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "new_key", "new_value"),
					testAccCheckCreatedAtAttr(redisResource),
					resource.TestCheckResourceAttr(redisResource, "security_group_ids.#", "1"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
		},
	})
}

// Test that a sharded Redis Cluster can be created, updated and destroyed
func TestAccMDBRedisCluster_sharded(t *testing.T) {
	t.Parallel()

	var r redis.Cluster
	redisName := acctest.RandomWithPrefix("tf-sharded-redis")
	redisDesc := "Sharded Redis Cluster Terraform Test"
	folderID := getExampleFolderID()
	version := "5.0"
	baseDiskSize := 100
	diskTypeId := "local-ssd"
	tlsEnabled := false

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckVPCNetworkDestroy,
		Steps: []resource.TestStep{
			// Create Redis Cluster
			{
				Config: testAccMDBRedisShardedClusterConfig(redisName, redisDesc, version, baseDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResourceSharded, &r, 6, tlsEnabled),
					resource.TestCheckResourceAttr(redisResourceSharded, "name", redisName),
					resource.TestCheckResourceAttr(redisResourceSharded, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResourceSharded, "description", redisDesc),
					testAccCheckMDBRedisClusterHasShards(&r, []string{"first", "second", "third"}),
					testAccCheckMDBRedisClusterHasResources(&r, "hm1.nano", baseDiskSize,
						diskTypeId),
					testAccCheckCreatedAtAttr(redisResourceSharded),
				),
			},
			mdbRedisClusterImportStep(redisResourceSharded),
			// Add new shard, delete old shard
			{
				Config: testAccMDBRedisShardedClusterConfigUpdated(redisName, redisDesc, version, baseDiskSize,
					diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResourceSharded, &r, 6, tlsEnabled),
					resource.TestCheckResourceAttr(redisResourceSharded, "name", redisName),
					resource.TestCheckResourceAttr(redisResourceSharded, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResourceSharded, "description", redisDesc),
					testAccCheckMDBRedisClusterHasShards(&r, []string{"first", "second", "new"}),
					testAccCheckMDBRedisClusterHasResources(&r, "hm1.nano", baseDiskSize,
						diskTypeId),
					testAccCheckCreatedAtAttr(redisResourceSharded),
				),
			},
			mdbRedisClusterImportStep(redisResourceSharded),
		},
	})
}

func TestAccMDBRedis6Cluster_full(t *testing.T) {
	t.Parallel()

	var r redis.Cluster
	redisName := acctest.RandomWithPrefix("tf-redis")
	redisDesc := "Redis 6 Cluster Terraform Test"
	redisDesc2 := "Redis 6 Cluster Terraform Test Updated"
	folderID := getExampleFolderID()
	version := "6.0"
	baseDiskSize := 100
	diskTypeId := "local-ssd"
	baseFlavor := "hm1.nano"
	tlsEnabled := true

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckVPCNetworkDestroy,
		Steps: []resource.TestStep{
			// Create Redis Cluster
			{
				Config: testAccMDBRedisClusterConfigMain(redisName, redisDesc, "PRESTABLE", false, &tlsEnabled, version,
					baseFlavor, baseDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 3, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "ALLKEYS_LRU", 100,
						"Elg", 5000, 10, 15, version),
					testAccCheckMDBRedisClusterHasResources(&r, baseFlavor, baseDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "test_key", "test_value"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.type", "WEEKLY"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.day", "FRI"),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.hour", "20"),
					testAccCheckCreatedAtAttr(redisResource),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// Change some options
			{
				Config: testAccMDBRedisClusterConfigUpdated(redisName, redisDesc2, &tlsEnabled, version, baseFlavor,
					baseDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 3, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc2),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "VOLATILE_LFU", 200,
						"Ex", 6000, 12, 17, version),
					testAccCheckMDBRedisClusterHasResources(&r, baseFlavor, baseDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "new_key", "new_value"),
					testAccCheckCreatedAtAttr(redisResource),
					resource.TestCheckResourceAttr(redisResource, "maintenance_window.0.type", "ANYTIME"),
				),
			},
			mdbRedisClusterImportStep(redisResource),
			// Add new host
			{
				Config: testAccMDBRedisClusterConfigAddedHost(redisName, redisDesc2, &tlsEnabled, version, baseFlavor,
					baseDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResource, &r, 4, tlsEnabled),
					resource.TestCheckResourceAttr(redisResource, "name", redisName),
					resource.TestCheckResourceAttr(redisResource, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResource, "description", redisDesc2),
					resource.TestCheckResourceAttrSet(redisResource, "host.0.fqdn"),
					resource.TestCheckResourceAttrSet(redisResource, "host.1.fqdn"),
					testAccCheckMDBRedisClusterHasConfig(&r, "VOLATILE_LFU", 200,
						"Ex", 6000, 12, 17, version),
					testAccCheckMDBRedisClusterHasResources(&r, baseFlavor, baseDiskSize, diskTypeId),
					testAccCheckMDBRedisClusterContainsLabel(&r, "new_key", "new_value"),
					testAccCheckCreatedAtAttr(redisResource),
				),
			},
			mdbRedisClusterImportStep(redisResource),
		},
	})
}

// Test that a sharded Redis Cluster can be created, updated and destroyed
func TestAccMDBRedis6Cluster_sharded(t *testing.T) {
	t.Parallel()

	var r redis.Cluster
	redisName := acctest.RandomWithPrefix("tf-sharded-redis")
	redisDesc := "Sharded Redis Cluster Terraform Test"
	folderID := getExampleFolderID()
	version := "6.0"
	baseDiskSize := 16
	diskTypeId := "network-ssd"
	tlsEnabled := false

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckVPCNetworkDestroy,
		Steps: []resource.TestStep{
			// Create Redis Cluster
			{
				Config: testAccMDBRedisShardedClusterConfig(redisName, redisDesc, version, baseDiskSize, diskTypeId),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResourceSharded, &r, 3, tlsEnabled),
					resource.TestCheckResourceAttr(redisResourceSharded, "name", redisName),
					resource.TestCheckResourceAttr(redisResourceSharded, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResourceSharded, "description", redisDesc),
					testAccCheckMDBRedisClusterHasShards(&r, []string{"first", "second", "third"}),
					testAccCheckMDBRedisClusterHasResources(&r, "hm1.nano", baseDiskSize,
						diskTypeId),
					testAccCheckCreatedAtAttr(redisResourceSharded),
				),
			},
			mdbRedisClusterImportStep(redisResourceSharded),
			// Add new shard, delete old shard
			{
				Config: testAccMDBRedisShardedClusterConfigUpdated(redisName, redisDesc, version, baseDiskSize, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMDBRedisClusterExists(redisResourceSharded, &r, 3, tlsEnabled),
					resource.TestCheckResourceAttr(redisResourceSharded, "name", redisName),
					resource.TestCheckResourceAttr(redisResourceSharded, "folder_id", folderID),
					resource.TestCheckResourceAttr(redisResourceSharded, "description", redisDesc),
					testAccCheckMDBRedisClusterHasShards(&r, []string{"first", "second", "new"}),
					testAccCheckMDBRedisClusterHasResources(&r, "hm1.nano", baseDiskSize,
						diskTypeId),
					testAccCheckCreatedAtAttr(redisResourceSharded),
				),
			},
			mdbRedisClusterImportStep(redisResourceSharded),
		},
	})
}

func testAccCheckMDBRedisClusterDestroy(s *terraform.State) error {
	config := testAccProvider.Meta().(*Config)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "yandex_mdb_redis_cluster" {
			continue
		}

		_, err := config.sdk.MDB().Redis().Cluster().Get(context.Background(), &redis.GetClusterRequest{
			ClusterId: rs.Primary.ID,
		})

		if err == nil {
			return fmt.Errorf("Redis Cluster still exists")
		}
	}

	return nil
}

func testAccCheckMDBRedisClusterExists(n string, r *redis.Cluster, hosts int, tlsEnabled bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		config := testAccProvider.Meta().(*Config)

		found, err := config.sdk.MDB().Redis().Cluster().Get(context.Background(), &redis.GetClusterRequest{
			ClusterId: rs.Primary.ID,
		})
		if err != nil {
			return err
		}

		if found.Id != rs.Primary.ID {
			return fmt.Errorf("Redis Cluster not found")
		}

		if found.TlsEnabled != tlsEnabled {
			return fmt.Errorf("tls mode: found = %t; expected = %t", found.TlsEnabled, tlsEnabled)
		}

		*r = *found

		resp, err := config.sdk.MDB().Redis().Cluster().ListHosts(context.Background(), &redis.ListClusterHostsRequest{
			ClusterId: rs.Primary.ID,
			PageSize:  defaultMDBPageSize,
		})
		if err != nil {
			return err
		}

		if len(resp.Hosts) != hosts {
			return fmt.Errorf("Expected %d hosts, got %d", hosts, len(resp.Hosts))
		}

		return nil
	}
}

func testAccCheckMDBRedisClusterHasShards(r *redis.Cluster, shards []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := testAccProvider.Meta().(*Config)

		resp, err := config.sdk.MDB().Redis().Cluster().ListShards(context.Background(), &redis.ListClusterShardsRequest{
			ClusterId: r.Id,
			PageSize:  defaultMDBPageSize,
		})
		if err != nil {
			return err
		}

		if len(resp.Shards) != len(shards) {
			return fmt.Errorf("Expected %d shards, got %d", len(shards), len(resp.Shards))
		}
		for _, s := range shards {
			found := false
			for _, rs := range resp.Shards {
				if s == rs.Name {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("Shard '%s' not found", s)
			}
		}
		return nil
	}
}

func testAccCheckMDBRedisClusterHasConfig(r *redis.Cluster, maxmemoryPolicy string, timeout int64,
	notifyKeyspaceEvents string, slowlogLogSlowerThan int64, slowlogMaxLen int64, databases int64,
	version string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := extractRedisConfig(r.Config)
		if c.maxmemoryPolicy != maxmemoryPolicy {
			return fmt.Errorf("Expected config.maxmemory_policy '%s', got '%s'", maxmemoryPolicy, c.maxmemoryPolicy)
		}
		if c.timeout != timeout {
			return fmt.Errorf("Expected config.timeout '%d', got '%d'", timeout, c.timeout)
		}
		if c.notifyKeyspaceEvents != notifyKeyspaceEvents {
			return fmt.Errorf("Expected config.notify_keyspace_events '%s', got '%s'", notifyKeyspaceEvents, c.notifyKeyspaceEvents)
		}
		if c.slowlogLogSlowerThan != slowlogLogSlowerThan {
			return fmt.Errorf("Expected config.slowlog_log_slower_than '%d', got '%d'", slowlogLogSlowerThan, c.slowlogLogSlowerThan)
		}
		if c.slowlogMaxLen != slowlogMaxLen {
			return fmt.Errorf("Expected config.slowlog_max_len '%d', got '%d'", slowlogMaxLen, c.slowlogMaxLen)
		}
		if c.databases != databases {
			return fmt.Errorf("Expected config.databases '%d', got '%d'", databases, c.databases)
		}
		if c.version != version {
			return fmt.Errorf("Expected config.version '%s', got '%s'", version, c.version)
		}
		return nil
	}
}

func testAccCheckMDBRedisClusterHasResources(r *redis.Cluster, resourcePresetID string, diskSizeGb int,
	diskTypeId string) resource.TestCheckFunc {
	diskSize := int64(diskSizeGb * int(math.Pow(2, 30)))
	return func(s *terraform.State) error {
		rs := r.Config.Resources
		if rs.ResourcePresetId != resourcePresetID {
			return fmt.Errorf("Expected resource preset id '%s', got '%s'", resourcePresetID, rs.ResourcePresetId)
		}
		if rs.DiskSize != diskSize {
			return fmt.Errorf("Expected label with key '%d', got '%d'", diskSize, rs.DiskSize)
		}
		if rs.DiskTypeId != diskTypeId {
			return fmt.Errorf("Expected label with key '%s', got '%s'", diskTypeId, rs.DiskTypeId)
		}
		return nil
	}
}

func testAccCheckMDBRedisClusterContainsLabel(r *redis.Cluster, key string, value string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, ok := r.Labels[key]
		if !ok {
			return fmt.Errorf("Expected label with key '%s' not found", key)
		}
		if v != value {
			return fmt.Errorf("Incorrect label value for key '%s': expected '%s' but found '%s'", key, value, v)
		}
		return nil
	}
}

// TODO: add more zones when v2 platform becomes available.
const redisVPCDependencies = `
resource "yandex_vpc_network" "foo" {}

resource "yandex_vpc_subnet" "foo" {
  zone           = "ru-central1-c"
  network_id     = "${yandex_vpc_network.foo.id}"
  v4_cidr_blocks = ["10.3.0.0/24"]
}

resource "yandex_vpc_security_group" "sg-x" {
  network_id     = "${yandex_vpc_network.foo.id}"
  ingress {
    protocol          = "ANY"
    description       = "Allow incoming traffic from members of the same security group"
    from_port         = 0
    to_port           = 65535
    v4_cidr_blocks    = ["0.0.0.0/0"]
  }
  egress {
    protocol          = "ANY"
    description       = "Allow outgoing traffic to members of the same security group"
    from_port         = 0
    to_port           = 65535
    v4_cidr_blocks    = ["0.0.0.0/0"]
  }
}

resource "yandex_vpc_security_group" "sg-y" {
  network_id     = "${yandex_vpc_network.foo.id}"
  
  ingress {
    protocol          = "ANY"
    description       = "Allow incoming traffic from members of the same security group"
    from_port         = 0
    to_port           = 65535
    v4_cidr_blocks    = ["0.0.0.0/0"]
  }
  egress {
    protocol          = "ANY"
    description       = "Allow outgoing traffic to members of the same security group"
    from_port         = 0
    to_port           = 65535
    v4_cidr_blocks    = ["0.0.0.0/0"]
  }
}
`

func getSentinelHosts(diskTypeId string) string {
	res := ""
	if diskTypeId == "local-ssd" {
		res = `
  host {
  	zone      = "ru-central1-c"
	subnet_id = "${yandex_vpc_subnet.foo.id}"
  }

  host {
  	zone      = "ru-central1-c"
	subnet_id = "${yandex_vpc_subnet.foo.id}"
  }

  host {
  	zone      = "ru-central1-c"
	subnet_id = "${yandex_vpc_subnet.foo.id}"
  }
`
	} else {
		res = `
  host {
  	zone      = "ru-central1-c"
	subnet_id = "${yandex_vpc_subnet.foo.id}"
  }
`
	}
	return res
}

func getShardedHosts(diskTypeId string, thirdShard string) string {
	res := ""
	if diskTypeId == "local-ssd" {
		res = fmt.Sprintf(`
  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "first"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "second"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "%s"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "first"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "second"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "%s"
  }
`, thirdShard, thirdShard)
	} else {
		res = fmt.Sprintf(`
  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "first"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "second"
  }

  host {
    zone       = "ru-central1-c"
    subnet_id  = "${yandex_vpc_subnet.foo.id}"
	shard_name = "%s"
  }
`, thirdShard)
	}
	return res
}

func getDiskTypeStr(diskTypeId string) string {
	diskTypeStr := ""
	if diskTypeId != "" {
		diskTypeStr = fmt.Sprintf(`
    disk_type_id       = "%s"
`, diskTypeId)
	}
	return diskTypeStr
}

func getTlsEnabled(tlsEnabled *bool) string {
	res := ""
	if tlsEnabled != nil {
		res = fmt.Sprintf("tls_enabled = %t", *tlsEnabled)
	}
	return res
}

func testAccMDBRedisClusterConfigMain(name, desc, environment string, deletionProtection bool, tlsEnabled *bool,
	version string, flavor string, diskSize int, diskTypeId string) string {
	return fmt.Sprintf(redisVPCDependencies+`
resource "yandex_mdb_redis_cluster" "foo" {
  name        = "%s"
  description = "%s"
  environment = "%s"
  network_id  = "${yandex_vpc_network.foo.id}"
%s

  labels = {
    test_key = "test_value"
  }

  config {
    password         = "passw0rd"
    timeout          = 100
    maxmemory_policy = "ALLKEYS_LRU"
	notify_keyspace_events = "Elg"
	slowlog_log_slower_than = 5000
	slowlog_max_len = 10
	databases = 15
	version			 = "%s"
  }

  resources {
    resource_preset_id = "%s"
    disk_size          = %d
%s
  }

%s

  security_group_ids = ["${yandex_vpc_security_group.sg-x.id}"]

  maintenance_window {
    type = "WEEKLY"
    day  = "FRI"
    hour = 20
  }
  
  deletion_protection = %t
}
`, name, desc, environment, getTlsEnabled(tlsEnabled), version, flavor, diskSize, getDiskTypeStr(diskTypeId), getSentinelHosts(diskTypeId), deletionProtection)
}

func testAccMDBRedisClusterConfigUpdated(name, desc string, tlsEnabled *bool, version string, flavor string, diskSize int,
	diskTypeId string) string {
	return fmt.Sprintf(redisVPCDependencies+`
resource "yandex_mdb_redis_cluster" "foo" {
  name        = "%s"
  description = "%s"
  environment = "PRESTABLE"
  network_id  = "${yandex_vpc_network.foo.id}"
%s

  labels = {
    new_key = "new_value"
  }

  config {
    password         = "passw0rd"
    timeout          = 200
    maxmemory_policy = "VOLATILE_LFU"
	notify_keyspace_events = "Ex"
	slowlog_log_slower_than = 6000
	slowlog_max_len = 12
	databases = 17
	version			 = "%s"
  }

  resources {
    resource_preset_id = "%s"
    disk_size          = %d
%s
  }

%s

  security_group_ids = ["${yandex_vpc_security_group.sg-x.id}", "${yandex_vpc_security_group.sg-y.id}"]

  maintenance_window {
    type = "ANYTIME"
  }
}
`, name, desc, getTlsEnabled(tlsEnabled), version, flavor, diskSize, getDiskTypeStr(diskTypeId),
		getSentinelHosts(diskTypeId))
}

func testAccMDBRedisClusterConfigAddedHost(name, desc string, tlsEnabled *bool, version string, flavor string, diskSize int,
	diskTypeId string) string {
	return fmt.Sprintf(redisVPCDependencies+`
resource "yandex_mdb_redis_cluster" "foo" {
  name        = "%s"
  description = "%s"
  environment = "PRESTABLE"
  network_id  = "${yandex_vpc_network.foo.id}"
%s

  labels = {
    new_key = "new_value"
  }

  config {
    password         = "passw0rd"
    timeout          = 200
    maxmemory_policy = "VOLATILE_LFU"
	notify_keyspace_events = "Ex"
	slowlog_log_slower_than = 6000
	slowlog_max_len = 12
	databases = 17
	version			 = "%s"
  }

  resources {
    resource_preset_id = "%s"
    disk_size          = %d
%s
  }

%s

  host {
    zone      = "ru-central1-c"
    subnet_id = "${yandex_vpc_subnet.foo.id}"
  }

  security_group_ids = ["${yandex_vpc_security_group.sg-y.id}"]
}
`, name, desc, getTlsEnabled(tlsEnabled), version, flavor, diskSize, getDiskTypeStr(diskTypeId),
		getSentinelHosts(diskTypeId))
}

func testAccMDBRedisShardedClusterConfig(name, desc string, version string, diskSize int, diskTypeId string) string {
	return fmt.Sprintf(redisVPCDependencies+`
resource "yandex_mdb_redis_cluster" "bar" {
  name        = "%s"
  description = "%s"
  environment = "PRESTABLE"
  network_id  = "${yandex_vpc_network.foo.id}"
  sharded     = true

  config {
    password = "passw0rd"
	version  = "%s"
  }

  resources {
    resource_preset_id = "hm1.nano"
    disk_size          = %d
%s
  }

%s
}
`, name, desc, version, diskSize, getDiskTypeStr(diskTypeId), getShardedHosts(diskTypeId, "third"))
}

func testAccMDBRedisShardedClusterConfigUpdated(name, desc string, version string, diskSize int,
	diskTypeId string) string {
	return fmt.Sprintf(redisVPCDependencies+`
resource "yandex_mdb_redis_cluster" "bar" {
  name        = "%s"
  description = "%s"
  environment = "PRESTABLE"
  network_id  = "${yandex_vpc_network.foo.id}"
  sharded     = true

  config {
    password = "passw0rd"
	version	 = "%s"
  }

  resources {
    resource_preset_id = "hm1.nano"
    disk_size          = %d
%s
  }

%s
}
`, name, desc, version, diskSize, getDiskTypeStr(diskTypeId), getShardedHosts(diskTypeId, "new"))
}
