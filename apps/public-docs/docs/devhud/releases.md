# DevHud Releases

DevHud releases are coordinated across desktop, mobile stores, Chrome, the API, updater, and documentation. There is no partial general-availability channel.

Desktop release evidence includes platform signatures, SHA-256 checksums, provenance, software-bill-of-materials data, and updater signatures. Updater manifests are verified from their separately published surface. The updater checks the installed package type, validates the stable manifest and artifact, and requires explicit approval before download, installation, and restart.

The updater trust root is pinned. Key rotation requires a signed successor chain, and rollback requires signed authorization. Mobile packages remain store-managed. Store review or release verification delays keep the coordinated release pending.

Use [Install and Verify](install) before opening a package. Report suspicious artifacts through [Security](security), and use [Support](support) for failed updates or restart-only recovery.
