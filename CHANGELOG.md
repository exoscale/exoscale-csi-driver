# Changelog

## v0.31.2

### Improvements

* container-storage-interface: upgrade to v1.11.0 #73
* egoscale: update to v3.1.9 to allow volumes of minimum size 1GiB #74 

## v0.31.1

### Improvements

* Deployment: Resolved an issue where volumes could become stuck in a Terminating or Released state, ensuring proper deletion and cleanup
* Reduce minimum blockstorage volume size to 1GB
* Deployment: Add node toleration exist #46
* doc: warn against manually modifying volumes through Exoscale API #44
* CI: action to manually destroy integ test cluster #41
* integ-tests: improve test stability #65

## 0.31.0

### Improvements

* Kubernetes SDK v0.31 #40
* GO 1.23.0 #40

## 0.29.6

### Improvements

* Driver: Meta Data fallback on CD-ROM for Private Instance #32
* goreleaser: set correct ldflags #29
* CSI: remove the beta notice #31
* go.mk: lint with staticcheck #30
* egoscale: update to v3.1.0 and fix #35

## 0.29.5

### Improvements

* Driver: Get rid of CCM dependency #28

### Bug fixes

* Controller: Remove panic in CreateSnapshot #27

## 0.29.4

### Improvements

* Driver: Implement Expand Volume #1

## 0.29.3

### Improvements

* Driver: Use egoscale ENV credential provider #24
* go.mk: remove submodule and initialize through make #15
* integ-tests: use IAMv3 API key #13
* document and minimize IAM rule policy for CSI #19
* integ-tests: use egoscale v3 #20
* integ-test: verify that retain policy is respected #22
* controller: accept size fields in GiB #26

## 0.29.2

### Bug fixes

* controller: fix frequent sidecar restarts #12

## 0.29.1

### Improvements

* Re-enable multizone fully supported (#9)
* split deployment manifests (#11)
* Project Status: beta phase (#10)
* Renaming on ENV and secret name (#7)
* Remove multizone and fix URL environment (#4)
* Vendor: Update egoscale v3 (#2)

## 0.29.0

### Features

* Initial CSI driver version
