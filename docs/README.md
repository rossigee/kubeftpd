# KubeFTPd Documentation

Start with the section that matches what you're doing.

## Deploy

- [Deployment](deployment.md): Helm install, load balancer and kubectl options
- [Gateway API](gateway-api.md): expose FTP through Gateway API instead of a LoadBalancer
- [Configuration](configuration.md): environment variables, built-in users, bind addresses

## Manage users and storage

- [Custom resources](custom-resources.md): `User`, `MinioBackend`, `WebDavBackend` and `FilesystemBackend`
- [Passwords](passwords.md): secret-based passwords, validation rules, migration, rotation and incident response
- [Examples](../examples/README.md): ready-to-edit manifests

## Operate

- [Monitoring](monitoring.md): logs, metrics, health endpoints and tracing
- [Troubleshooting](troubleshooting.md): common failures and fixes

## Other

- [Migration from SFTPGo](migration-from-sftpgo.md)
- [Development](development.md): building, testing and linting
- [Changelog](../CHANGELOG.md)
