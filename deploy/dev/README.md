# Berkas khusus pengembangan

`lajur-dev.private.pem` adalah kunci privat License Console **tiruan** (kid `lajur-dev`).
Pasangan publiknya ada di `internal/platform/license/keys/dev/` dan hanya dipercaya build
pengembangan; build rilis (`-tags release`) tidak membawanya. Jangan pernah memakai kunci ini
untuk License Console sungguhan; buat kunci produksi dengan `lajur license-keygen`.

Kunci privat di folder ini tidak disimpan di git (`.gitignore`). Salin dari laptop pengembang lain,
atau buat pasangan baru dengan `lajur license-keygen` lalu ganti kunci publik di
`internal/platform/license/keys/dev/`.
