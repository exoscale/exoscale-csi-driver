# Changelog


## v0.29.7
- 2628ec3 Prepare release
- a99cba2 Integ Test: Fix zone implementation (#37)
- 34078cc Refacto tearDownCSI path
- b7c6174 Integ test refacto path join

## v0.29.6
- b700c10 Prepare release
- 115d80a typo in json IAM Role example (#36)
- 948bd45 egoscale: update to v3.1.0 and fix (#35)
- 3853bf6 Driver: Meta Data fallback on CD-ROM for Private Instance (#32)
- 6db5cbf egoscale: use stable release v3.0.0 (#34)
- 17c12a8 go.mk: lint with staticcheck (#30)
- 04cd291 CSI: remove the beta notice (#31)
- c5f7837 goreleaser: set correct ldflags (#29)

## v0.29.5
- 8bf6da9 Prepare release
- 691f750 Driver: Get rid of CCM dependency (#28)
- 4efb361 Controller bug(fix): Remove panic in CreateSnapshot (#27)

## v0.29.4
- 23809b0 Prepare release
- 6ad189a Driver: Implement Expand Volume (#1)

## v0.29.3
- 61f9d24 Prepare release
- e8a9873 controller: accept size fields in GiB (#26)
- 8230aa7 Driver: Use egoscale ENV credential provider (#25)
- 4781975 integ-test: verify that retain policy is respected (#22)
- bda9167 integ-tests: use egoscale v3 (#20)
- f40ebce document and minimize IAM rule policy for CSI (#19)
- 96b5084 go.mk: remove submodule and initialize through make (#15)
- 7c33acc integ-tests: use IAMv3 API key (#13)
- 2973d79 Bump egoscale v3: Volume size validation change (#14)

## v0.29.2
- 936929a Prepare release
- 78eaf5e controller: fix frequent sidecar restarts (#12)

## v0.29.1
- e526506 Prepare release
- a89a443 Re-enable multizone fully supported (#9)
- d7090e0 split deployment manifests (#11)
- ad33d0a Project Status: beta phase (#10)
- 97df35c Renaming on ENV and secret name (#7)
- fc86721 Remove multizone and fix URL environment (#4)
- 4ad2072 Update README.md
- 7993c20 integ-test(fix): re-enable secrets  (#5)
- e46c999 Vendor: Update egoscale v3 (#2)

## v0.29.0
- fd62922 Initial CSI driver version
