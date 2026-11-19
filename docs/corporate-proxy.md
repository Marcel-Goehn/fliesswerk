# Behind a corporate proxy

This guide gets the Factory working behind a TLS-inspecting proxy such as Zscaler. It covers the two forms of Zscaler, PAC-only setups, `ca_certs`, runtimes that ignore `ca_certs`, and getting your container engine to trust the proxy.

The Factory supports proxies that need no login. Basic, NTLM and Kerberos proxy authentication and PAC files are not supported.

## Which setup do you have?

| Setup | How you recognise it | What to configure |
| --- | --- | --- |
| Zscaler, transparent | No proxy variables are set, and traffic is inspected anyway | `ca_certs` |
| Zscaler, explicit | `HTTPS_PROXY` is set in your shell | `ca_certs`. The Factory picks the variables up |
| PAC file only | Your OS has a proxy auto-config URL and no variables are set | `ca_certs`, and name the proxy yourself (see below) |

## Trusting the corporate root

Add the root certificate to `.fliesswerk/config.toml` as a PEM file:

```toml
[network]
ca_certs = ["certs/corporate-root.pem"]
```

A path can be absolute, start with `~/`, or be relative to the project root. List several files if you have several roots.

`fliesswerk check` tells you when you need this. It makes one request from inside a Sandbox to the Harness's endpoint, and if the Sandbox rejects the certificate while your machine accepts it, it names the root your machine verified and prints this fix. Every Run does the same when it starts, and stops if the check fails. The request uses `curl`, so an image without `curl` gets a warning and no check.

### Exporting the root

macOS:

```sh
security find-certificate -c "Zscaler Root CA" -p /Library/Keychains/System.keychain > corporate-root.pem
```

Windows (PowerShell):

```powershell
$cert = Get-ChildItem Cert:\LocalMachine\Root | Where-Object Subject -match "Zscaler" | Select-Object -First 1
Export-Certificate -Cert $cert -FilePath root.cer
certutil -encode root.cer corporate-root.pem
```

Linux: your IT department usually installs it under `/usr/local/share/ca-certificates` or `/etc/pki/ca-trust/source/anchors`. Copy that file. If it is in DER form, convert it with `openssl x509 -inform der -in root.cer -out corporate-root.pem`.

### What `ca_certs` does in a Sandbox

When a Sandbox is created, the Factory builds a file holding the Sandbox's own system list plus `ca_certs`.

| Variable | Points at |
| --- | --- |
| `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `AWS_CA_BUNDLE`, `GIT_SSL_CAINFO`, `CURL_CA_BUNDLE` | The combined file |
| `NODE_EXTRA_CA_CERTS` | `ca_certs` only |

These override the same names in `[sandbox.env]`. With no `ca_certs`, the Factory sets none of them.

A container image needs a system list to add to. Without one the Sandbox stops and asks you to install `ca-certificates`.

## Naming the proxy

Sandboxes never use your proxy directly. `HTTPS_PROXY` inside one always points at the Factory's egress proxy, which chains to your corporate proxy.

The egress proxy follows your host's `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. To use a different proxy, or if you only have a PAC file, either set `HTTPS_PROXY` in your shell or set this in `.fliesswerk/config.toml`:

```toml
[network]
upstream_proxy = "http://proxy.corp.example:8080"
```

`upstream_proxy` replaces all three host variables. To find the proxy behind a PAC file, open the PAC URL and look for the `PROXY host:port` it returns for external hosts.

A proxy that listens on your machine's own localhost works with a native Sandbox only. A container Sandbox reaches the outside through a proxy sidecar, and in a container `localhost` is that container, so `fliesswerk check` stops and tells you. Make the proxy listen on an address containers can reach and name that in `upstream_proxy`, or use `kind = "native"`.

## Image builds

Builds go through your corporate proxy, not the egress proxy. They get your proxy variables as build args (`upstream_proxy` instead, if set) and `ca_certs` as the build secret `fliesswerk-ca`. Changing `ca_certs` rebuilds the image.

Install the secret early in `.fliesswerk/Containerfile`, so that package installs trust the proxy. On Debian or Ubuntu:

```dockerfile
RUN --mount=type=secret,id=fliesswerk-ca sh -c 'if [ -s /run/secrets/fliesswerk-ca ]; then cp /run/secrets/fliesswerk-ca /usr/local/share/ca-certificates/fliesswerk-ca.crt && update-ca-certificates; fi'
```

The image must already have `ca-certificates` at that point. The secret is mounted only for that `RUN`, and it stays out of the image's layers.

## Runtimes that ignore these variables

### Java

The JVM uses its own keystore. In a container Sandbox, import the root during the build:

```dockerfile
RUN --mount=type=secret,id=fliesswerk-ca keytool -importcert -noprompt -trustcacerts -alias corporate-root -file /run/secrets/fliesswerk-ca -keystore "$JAVA_HOME/lib/security/cacerts" -storepass changeit
```

`keytool` imports only the first certificate of a file. For several roots, split them and import each one.

In a native Sandbox, build a keystore outside the project, give the Sandbox read access to it and point the JVM at it:

```toml
[sandbox]
read_only_paths = ["~/.fliesswerk-java/cacerts"]

[sandbox.env]
JAVA_TOOL_OPTIONS = "-Djavax.net.ssl.trustStore=/home/you/.fliesswerk-java/cacerts"
```

### .NET

On Linux, .NET reads the OpenSSL store, so `SSL_CERT_FILE` usually covers it. If a tool still rejects the certificate, install the root into the image's system store as shown under [Image builds](#image-builds).

On macOS, .NET uses the Keychain, which a native Sandbox can't reach. Use a container Sandbox for .NET work on a Mac behind an inspecting proxy.

## Making the container engine trust the proxy

`ca_certs` covers Sandboxes and builds, but the engine pulls base images itself. If a pull fails with `x509: certificate signed by unknown authority`, the engine needs the root too.

### Podman machine (macOS, Windows)

```sh
podman machine ssh "sudo tee /etc/pki/ca-trust/source/anchors/corporate-root.pem" < corporate-root.pem
podman machine ssh "sudo update-ca-trust"
podman machine stop && podman machine start
```

### Docker Desktop

On macOS, add the root to the System keychain and restart Docker Desktop:

```sh
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain corporate-root.pem
```

On Windows, import it into **Trusted Root Certification Authorities** for the local machine and restart Docker Desktop.

Docker Desktop also has its own proxy settings under **Settings > Resources > Proxies**, which apply to image pulls.

### Docker or Podman on Linux

Add the root to the system store, then restart Docker (Podman has no daemon to restart):

```sh
# Debian, Ubuntu
sudo cp corporate-root.pem /usr/local/share/ca-certificates/corporate-root.crt && sudo update-ca-certificates
# Fedora, RHEL
sudo cp corporate-root.pem /etc/pki/ca-trust/source/anchors/ && sudo update-ca-trust
```
