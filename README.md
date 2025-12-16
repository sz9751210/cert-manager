# CertManager - SSL & Domain Lifecycle Monitor
> Enterprise-grade SSL certificate and domain monitoring solution with Cloudflare integration.

[English](#-certmanager---english) | [中文說明](#-certmanager---中文介紹)

---

## 🇬🇧 CertManager - English

**CertManager** is a comprehensive DevOps tool designed to automate the monitoring of SSL/TLS certificates and domain registration status. Unlike traditional monitoring tools, CertManager integrates directly with **Cloudflare**, automatically synchronizing your domain list without manual entry.

It provides a unified dashboard to track SSL expiry, TLS security grades, HTTP uptime, and Domain WHOIS status, complete with multi-channel alerting.

### ✨ Key Features

* **🔄 Auto-Sync with Cloudflare**: Automatically fetches all zones and records from your Cloudflare account. Zero manual data entry.
* **🛡️ Deep SSL/TLS Inspection**:
    * Monitors Certificate Expiry (Days remaining).
    * **TLS Protocol Version** check (Alerts on TLS 1.0/1.1).
    * **SANs (Subject Alternative Names)** visibility.
    * **Certificate Change History**: Timeline view of fingerprint changes.
* **🌍 Domain & Uptime Monitoring**:
    * **WHOIS Monitoring**: Tracks domain registration expiry.
    * **HTTP Uptime Check**: Monitors latency and HTTP status codes (200, 404, 500).
* **⚡ Management & Efficiency**:
    * **Bulk Actions**: Batch ignore/monitor domains.
    * **CSV Export**: One-click report generation for audits.
    * **Smart Filtering**: Filter by Proxy status (Orange/Grey cloud), Zone, or Health status.
* **🔔 Smart Alerts**:
    * Supports **Telegram** and **Webhooks** (Slack/Teams/Discord).
    * Configurable thresholds (SSL < 14 days, Domain < 30 days).
    * Daily Cron scheduler for background scanning.
* **🔐 Enterprise Security**:
    * JWT-based Authentication.
    * Dockerized deployment.

### 🛠 Tech Stack

* **Backend**: Go (Golang) + Gin Framework + MongoDB Driver
* **Frontend**: React + TypeScript + Vite + Ant Design
* **Database**: MongoDB
* **Scheduler**: Robfig Cron
* **Container**: Docker & Docker Compose

### 🚀 Getting Started (Docker)

The easiest way to run CertManager is using Docker Compose.

#### 1. Prerequisites
* Docker & Docker Compose installed.
* A Cloudflare API Token (Read permissions for Zone and DNS).

#### 2. Installation
```bash
# Clone the repository
git clone [https://github.com/your-username/cert-manager.git](https://github.com/your-username/cert-manager.git)
cd cert-manager

# Update config (Optional, or use ENV variables)
# vim config/config.yaml
```
#### 3. Configuration
Edit docker-compose.yml or set environment variables:

```yaml
environment:
  - CLOUDFLARE_API_TOKEN=your_cf_api_token_here
  - MONGODB_URI=mongodb://mongo:27017
```
4. Run
5. 
```shell
docker-compose up -d --build
```
Access the dashboard at http://localhost.

Default User: admin

Default Password: admin123 (Please change this immediately in settings)