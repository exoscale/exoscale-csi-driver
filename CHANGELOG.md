# Changelog

## Unreleased

### Improvements

* goreleaser: set correct ldflags #29 
* CSI: remove the beta notice #31 
* go.mk: lint with staticcheck #30 

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
