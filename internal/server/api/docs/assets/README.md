# Vendored Swagger UI assets

`swagger-ui.css` and `swagger-ui-bundle.js` are unmodified files from the
upstream **swagger-ui-dist** npm package (Apache-2.0, see `LICENSE` / `NOTICE`).
`index.html` is written by this project.

| Item | Value |
|---|---|
| Package | `swagger-ui-dist` |
| Version | **5.32.6** |
| Tarball | `https://registry.npmjs.org/swagger-ui-dist/-/swagger-ui-dist-5.32.6.tgz` |
| Tarball SHA256 | `2d51e76c9092b38f23b48375b91587fbe97374794e9aa421ea5030b989872762` |
| `swagger-ui.css` SHA256 | `ca238f7d7c2cf4480c1e77a9c3b9da915ab216e96ffd354e69076560c650c6de` |
| `swagger-ui-bundle.js` SHA256 | `4b5a9b35a1adf37f00d39c0bae23cdb37007e2946240ef035137cc85b4d55349` |

## How to upgrade

```bash
V=<new 5.x version>
curl -sLO https://registry.npmjs.org/swagger-ui-dist/-/swagger-ui-dist-$V.tgz
sha256sum swagger-ui-dist-$V.tgz   # record below the table above
tar xzf swagger-ui-dist-$V.tgz package/swagger-ui.css package/swagger-ui-bundle.js package/LICENSE package/NOTICE
cp package/{swagger-ui.css,swagger-ui-bundle.js,LICENSE,NOTICE} internal/server/api/docs/assets/
sha256sum internal/server/api/docs/assets/{swagger-ui.css,swagger-ui-bundle.js}
# update this README, run `go test ./internal/server/api/...`, eyeball /api/docs/ once
```

Only these two runtime files are vendored; maps, presets and the upstream
`index.html` are intentionally excluded (the page is served by our own
`index.html` with relative URLs and no topbar).
