use crate::config::Config;
use serde::Deserialize;
use serde_json::Value;
use std::time::Duration;

/// Cấu trúc node trả về từ Proxmox API `GET /api2/json/nodes`
#[derive(serde::Deserialize, Debug, Clone)]
pub(crate) struct ProxmoxNode {
    /// Định danh node vật lý trong Proxmox (ví dụ: "pve-node-01")
    pub node: String,
    /// Trạng thái của node theo Proxmox: "online" | "offline" | "unknown"
    pub status: String,
    /// Mức độ sử dụng CPU hiện tại (float 0.0–1.0, tỉ lệ trên maxcpu)
    #[serde(default)]
    pub cpu: f64,
    /// Tổng số lõi CPU vật lý
    #[serde(default)]
    pub maxcpu: u64,
    /// Bộ nhớ RAM đang sử dụng (bytes)
    #[serde(default)]
    pub mem: u64,
    /// Tổng dung lượng RAM vật lý (bytes)
    #[serde(default)]
    pub maxmem: u64,
    /// Dung lượng disk OS node đang dùng (bytes)
    #[serde(default)]
    pub disk: u64,
    /// Tổng dung lượng disk OS node (bytes)
    #[serde(default)]
    pub maxdisk: u64,
}

/// Wrapper lớp ngoài của Proxmox API response envelope
#[derive(Deserialize)]
struct ProxmoxApiResponse<T> {
    data: T,
}

#[derive(Deserialize)]
struct ClusterVMResource {
    #[serde(default)]
    vmid: u64,
    #[serde(default)]
    name: String,
    #[serde(default)]
    node: String,
    #[serde(default)]
    template: u8,
    #[serde(rename = "type", default)]
    resource_type: String,
}

#[derive(Clone, Debug)]
pub(crate) struct ProxmoxVm {
    pub(crate) vmid: u64,
    pub(crate) name: String,
    pub(crate) node: String,
    pub(crate) is_template: bool,
}

pub(super) struct CloneTemplateRequest<'a> {
    pub(super) template_node: &'a str,
    pub(super) template_vmid: u64,
    pub(super) target_node: &'a str,
    pub(super) target_vmid: u64,
    pub(super) provider_name: &'a str,
    pub(super) storage: &'a str,
    pub(super) pool: &'a str,
}

#[derive(Deserialize)]
pub(crate) struct ProxmoxVmConfig {
    #[serde(default)]
    pub(crate) description: Option<String>,
    #[serde(default)]
    pub(crate) bootdisk: Option<String>,
    #[serde(default)]
    pub(crate) scsi0: Option<String>,
    #[serde(default)]
    pub(crate) virtio0: Option<String>,
    #[serde(default)]
    pub(crate) sata0: Option<String>,
}

#[derive(Deserialize)]
struct TaskStatus {
    status: String,
    #[serde(default)]
    exitstatus: String,
}

#[derive(Deserialize)]
struct VMStatus {
    status: String,
}

/// Client kết nối chuyên biệt đến Proxmox REST API
pub struct ProxmoxClient {
    client: reqwest::Client,
    api_url: String,
    api_token: String,
    task_timeout: Duration,
}

impl ProxmoxClient {
    /// Khởi tạo client kết nối với cấu hình TLS thích hợp
    pub fn new(config: &Config) -> Result<Self, String> {
        let mut builder = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(20));

        // Chỉ cho phép skip TLS verify trên dev/test — production bắt buộc tắt
        if config.proxmox_tls_insecure {
            builder = builder.danger_accept_invalid_certs(true);
        }

        let client = builder
            .build()
            .map_err(|error| format!("build Proxmox HTTP client failed: {error}"))?;

        Ok(Self {
            client,
            api_url: config.proxmox_api_url.clone(),
            api_token: config.proxmox_api_token.clone(),
            task_timeout: Duration::from_secs(config.proxmox_task_timeout_seconds),
        })
    }

    /// Poll Proxmox REST API `/api2/json/nodes` để lấy danh sách node và metrics
    pub async fn fetch_nodes(&self) -> Result<Vec<ProxmoxNode>, String> {
        let url = format!("{}/api2/json/nodes", self.api_url.trim_end_matches('/'));

        let request = self
            .client
            .get(&url)
            // [COMMENT]: Proxmox yêu cầu Authorization header dạng: PVEAPIToken=user@realm!id=secret
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.nodes",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new("url.template", "/api2/json/nodes"),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_NODES_TRANSPORT_FAILED".to_string())?;

        if !response.status().is_success() {
            // Provider bodies are outside Aurora's trust boundary and may
            // contain infrastructure details. They must not enter job results.
            return Err(format!(
                "query Proxmox nodes returned HTTP {}",
                response.status()
            ));
        }

        let api_resp: ProxmoxApiResponse<Vec<ProxmoxNode>> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_NODES_RESPONSE_INVALID".to_string())?;

        Ok(api_resp.data)
    }

    pub async fn list_vms(&self) -> Result<Vec<ProxmoxVm>, String> {
        let url = format!(
            "{}/api2/json/cluster/resources?type=vm",
            self.api_url.trim_end_matches('/')
        );
        let request = self
            .client
            .get(&url)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.vm_inventory",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/cluster/resources?type=vm",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_INVENTORY_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("query Proxmox VM inventory returned HTTP {status}"));
        }
        let response: ProxmoxApiResponse<Vec<ClusterVMResource>> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_VM_INVENTORY_RESPONSE_INVALID".to_string())?;
        Ok(response
            .data
            .into_iter()
            .filter(|item| item.resource_type == "qemu")
            .map(|item| ProxmoxVm {
                vmid: item.vmid,
                name: item.name,
                node: item.node,
                is_template: item.template == 1,
            })
            .collect())
    }

    pub(super) async fn clone_template(
        &self,
        clone: CloneTemplateRequest<'_>,
    ) -> Result<String, String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/clone",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(clone.template_node),
            clone.template_vmid
        );
        let mut form = vec![
            ("newid", clone.target_vmid.to_string()),
            ("name", clone.provider_name.to_string()),
            ("target", clone.target_node.to_string()),
            ("full", "1".to_string()),
        ];
        if !clone.storage.is_empty() {
            form.push(("storage", clone.storage.to_string()));
        }
        if !clone.pool.is_empty() {
            form.push(("pool", clone.pool.to_string()));
        }
        let request = self
            .client
            .post(&url)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "POST proxmox.vm_clone",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/clone",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_CLONE_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("clone Proxmox template returned HTTP {status}"));
        }
        let response: ProxmoxApiResponse<String> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_VM_CLONE_RESPONSE_INVALID".to_string())?;
        Ok(response.data)
    }

    pub async fn configure_vm(
        &self,
        node: &str,
        vmid: u64,
        cpu_cores: u32,
        memory_mb: u64,
        ssh_public_key: &str,
        config_hash_hex: &str,
    ) -> Result<(), String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/config",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let form = [
            ("cores", cpu_cores.to_string()),
            ("memory", memory_mb.to_string()),
            ("sshkeys", ssh_public_key.to_string()),
            ("ipconfig0", "ip=dhcp".to_string()),
            ("agent", "enabled=1".to_string()),
            (
                "description",
                format!("Managed by Aurora\\naurora-config-sha256={config_hash_hex}"),
            ),
        ];
        let request = self
            .client
            .put(&url)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "PUT proxmox.vm_config",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "PUT"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/config",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_CONFIGURE_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("configure Proxmox VM returned HTTP {status}"));
        }
        Ok(())
    }

    pub async fn vm_config(&self, node: &str, vmid: u64) -> Result<ProxmoxVmConfig, String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/config",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let request = self
            .client
            .get(&url)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.vm_config",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/config",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_CONFIG_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("query Proxmox VM config returned HTTP {status}"));
        }
        let response: ProxmoxApiResponse<ProxmoxVmConfig> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_VM_CONFIG_RESPONSE_INVALID".to_string())?;
        Ok(response.data)
    }

    pub async fn resize_boot_disk(
        &self,
        node: &str,
        vmid: u64,
        boot_disk: &str,
        expansion_gb: u64,
    ) -> Result<(), String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/resize",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let form = [
            ("disk", boot_disk.to_string()),
            ("size", format!("+{expansion_gb}G")),
        ];
        let request = self
            .client
            .put(&url)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "PUT proxmox.vm_resize",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "PUT"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/resize",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_RESIZE_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("resize Proxmox VM disk returned HTTP {status}"));
        }
        Ok(())
    }

    pub async fn vm_status(&self, node: &str, vmid: u64) -> Result<String, String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/status/current",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let request = self
            .client
            .get(&url)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.vm_status",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/status/current",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_STATUS_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("query Proxmox VM status returned HTTP {status}"));
        }
        let response: ProxmoxApiResponse<VMStatus> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_VM_STATUS_RESPONSE_INVALID".to_string())?;
        Ok(response.data.status)
    }

    pub async fn start_vm(&self, node: &str, vmid: u64) -> Result<String, String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/status/start",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let request = self
            .client
            .post(&url)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "POST proxmox.vm_start",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/status/start",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_VM_START_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            let status = response.status();
            return Err(format!("start Proxmox VM returned HTTP {status}"));
        }
        let response: ProxmoxApiResponse<String> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_VM_START_RESPONSE_INVALID".to_string())?;
        Ok(response.data)
    }

    pub async fn download_url_to_storage(
        &self,
        node: &str,
        storage: &str,
        filename: &str,
        url: &str,
        checksum_hex: &str,
    ) -> Result<String, String> {
        let endpoint = format!(
            "{}/api2/json/nodes/{}/storage/{}/download-url",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            urlencoding::encode(storage)
        );
        let form = [
            ("content", "iso".to_string()),
            ("filename", filename.to_string()),
            ("url", url.to_string()),
            ("checksum", checksum_hex.to_string()),
            ("checksum-algorithm", "sha256".to_string()),
        ];
        let request = self
            .client
            .post(&endpoint)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "POST proxmox.image_download",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/storage/{storage}/download-url",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_IMAGE_DOWNLOAD_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            return Err(format!(
                "download image into Proxmox storage returned HTTP {}",
                response.status()
            ));
        }
        response
            .json::<ProxmoxApiResponse<String>>()
            .await
            .map(|response| response.data)
            .map_err(|_| "PROXMOX_IMAGE_DOWNLOAD_RESPONSE_INVALID".to_string())
    }

    pub async fn create_vm_from_import(
        &self,
        node: &str,
        vmid: u64,
        provider_name: &str,
        source_storage: &str,
        filename: &str,
        target_storage: &str,
        checksum_hex: &str,
    ) -> Result<String, String> {
        let endpoint = format!(
            "{}/api2/json/nodes/{}/qemu",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node)
        );
        let imported_volume = format!("{source_storage}:iso/{filename}");
        let boot_volume = format!("{target_storage}:0,import-from={imported_volume}");
        let description = format!("Managed by Aurora\\naurora-image-sha256={checksum_hex}");
        let form = [
            ("vmid", vmid.to_string()),
            ("name", provider_name.to_string()),
            ("scsihw", "virtio-scsi-single".to_string()),
            ("scsi0", boot_volume),
            ("boot", "order=scsi0".to_string()),
            ("agent", "enabled=1".to_string()),
            ("description", description),
        ];
        let request = self
            .client
            .post(&endpoint)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "POST proxmox.image_import",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new("url.template", "/api2/json/nodes/{node}/qemu"),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_IMAGE_IMPORT_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            return Err(format!(
                "create Proxmox image VM returned HTTP {}",
                response.status()
            ));
        }
        response
            .json::<ProxmoxApiResponse<String>>()
            .await
            .map(|response| response.data)
            .map_err(|_| "PROXMOX_IMAGE_IMPORT_RESPONSE_INVALID".to_string())
    }

    pub async fn convert_to_template(&self, node: &str, vmid: u64) -> Result<String, String> {
        let endpoint = format!(
            "{}/api2/json/nodes/{}/qemu/{}/template",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let request = self
            .client
            .post(&endpoint)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "POST proxmox.image_template",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/template",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_IMAGE_TEMPLATE_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            return Err(format!(
                "convert Proxmox image VM to template returned HTTP {}",
                response.status()
            ));
        }
        response
            .json::<ProxmoxApiResponse<String>>()
            .await
            .map(|response| response.data)
            .map_err(|_| "PROXMOX_IMAGE_TEMPLATE_RESPONSE_INVALID".to_string())
    }

    pub async fn delete_vm(&self, node: &str, vmid: u64) -> Result<String, String> {
        let endpoint = format!(
            "{}/api2/json/nodes/{}/qemu/{}",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let form = [
            ("purge", "1".to_string()),
            ("destroy-unreferenced-disks", "1".to_string()),
        ];
        let request = self
            .client
            .delete(&endpoint)
            .header("Authorization", &self.api_token)
            .form(&form);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "DELETE proxmox.image_template",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "DELETE"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new("url.template", "/api2/json/nodes/{node}/qemu/{vmid}"),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_IMAGE_DELETE_TRANSPORT_FAILED".to_string())?;
        if response.status() == reqwest::StatusCode::NOT_FOUND {
            return Ok(String::new());
        }
        if !response.status().is_success() {
            return Err(format!(
                "delete Proxmox image template returned HTTP {}",
                response.status()
            ));
        }
        response
            .json::<ProxmoxApiResponse<String>>()
            .await
            .map(|response| response.data)
            .map_err(|_| "PROXMOX_IMAGE_DELETE_RESPONSE_INVALID".to_string())
    }

    pub async fn delete_storage_content(
        &self,
        node: &str,
        storage: &str,
        filename: &str,
    ) -> Result<String, String> {
        let volume = format!("{storage}:iso/{filename}");
        let endpoint = format!(
            "{}/api2/json/nodes/{}/storage/{}/content/{}",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            urlencoding::encode(storage),
            urlencoding::encode(&volume)
        );
        let request = self
            .client
            .delete(&endpoint)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "DELETE proxmox.image_staging",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "DELETE"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/storage/{storage}/content/{volume}",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_IMAGE_STAGING_DELETE_TRANSPORT_FAILED".to_string())?;
        if response.status() == reqwest::StatusCode::NOT_FOUND {
            return Ok(String::new());
        }
        if !response.status().is_success() {
            return Err(format!(
                "delete Proxmox staged image returned HTTP {}",
                response.status()
            ));
        }
        response
            .json::<ProxmoxApiResponse<String>>()
            .await
            .map(|response| response.data)
            .map_err(|_| "PROXMOX_IMAGE_STAGING_DELETE_RESPONSE_INVALID".to_string())
    }

    pub async fn wait_task(&self, node: &str, upid: &str) -> Result<(), String> {
        let deadline = tokio::time::Instant::now() + self.task_timeout;
        loop {
            if tokio::time::Instant::now() >= deadline {
                return Err("Proxmox task exceeded the bounded execution deadline".to_string());
            }
            let url = format!(
                "{}/api2/json/nodes/{}/tasks/{}/status",
                self.api_url.trim_end_matches('/'),
                urlencoding::encode(node),
                urlencoding::encode(upid)
            );
            let request = self
                .client
                .get(&url)
                .header("Authorization", &self.api_token);
            let response = crate::observability::otel::OtelTracer::trace_http_request(
                "GET proxmox.task_status",
                vec![
                    opentelemetry::KeyValue::new("http.request.method", "GET"),
                    opentelemetry::KeyValue::new("server.address", "proxmox"),
                    opentelemetry::KeyValue::new(
                        "url.template",
                        "/api2/json/nodes/{node}/tasks/{upid}/status",
                    ),
                ],
                request,
            )
            .await
            .map_err(|_| "PROXMOX_TASK_POLL_TRANSPORT_FAILED".to_string())?;
            if !response.status().is_success() {
                let status = response.status();
                return Err(format!("poll Proxmox task returned HTTP {status}"));
            }
            let response: ProxmoxApiResponse<TaskStatus> = response
                .json()
                .await
                .map_err(|_| "PROXMOX_TASK_POLL_RESPONSE_INVALID".to_string())?;
            if response.data.status == "stopped" {
                if response.data.exitstatus == "OK" {
                    return Ok(());
                }
                return Err("Proxmox task stopped without successful completion".to_string());
            }
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
    }

    pub async fn guest_ipv4(&self, node: &str, vmid: u64) -> Result<String, String> {
        let url = format!(
            "{}/api2/json/nodes/{}/qemu/{}/agent/network-get-interfaces",
            self.api_url.trim_end_matches('/'),
            urlencoding::encode(node),
            vmid
        );
        let request = self
            .client
            .get(&url)
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.guest_ipv4",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new(
                    "url.template",
                    "/api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces",
                ),
            ],
            request,
        )
        .await
        .map_err(|_| "PROXMOX_GUEST_NETWORK_TRANSPORT_FAILED".to_string())?;
        if !response.status().is_success() {
            return Ok(String::new());
        }
        let response: ProxmoxApiResponse<Value> = response
            .json()
            .await
            .map_err(|_| "PROXMOX_GUEST_NETWORK_RESPONSE_INVALID".to_string())?;
        let interfaces = response
            .data
            .get("result")
            .and_then(Value::as_array)
            .or_else(|| response.data.as_array());
        let Some(interfaces) = interfaces else {
            return Ok(String::new());
        };
        for interface in interfaces {
            let addresses = interface
                .get("ip-addresses")
                .and_then(Value::as_array)
                .or_else(|| interface.get("ip_addresses").and_then(Value::as_array));
            let Some(addresses) = addresses else {
                continue;
            };
            for address in addresses {
                let value = address
                    .get("ip-address")
                    .and_then(Value::as_str)
                    .or_else(|| address.get("ip_address").and_then(Value::as_str))
                    .unwrap_or_default();
                if let Ok(parsed) = value.parse::<std::net::Ipv4Addr>() {
                    if !parsed.is_loopback() && !parsed.is_link_local() {
                        return Ok(parsed.to_string());
                    }
                }
            }
        }
        Ok(String::new())
    }
}
