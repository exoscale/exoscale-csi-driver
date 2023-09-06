# Integration Tests

The integration tests are executed in a GitHub Action on every commit that is pushed to any branch.
They always reuse the same cluster so they are in a concurrency group and you may have to wait until your PR gets tested.

If you would like to run the tests on your own locally, you will need an exoscale API key(IAMv3, not legacy) in your environment (EXOSCALE_API_KEY, EXOSCALE_API_SECRET).
You can run the tests like this.
```shell
go test \
            -v \
            --create-cluster \
            --create-csi-secret \
            --tear-down-csi \
            --image exoscale/csi-driver-integ-test \
            --cluster-name my-test-cluster \
            --zone ch-gva-2
```

All the other flags of the `go test` command are still available to you. Like for example the `-run '^$'` flag can be used to run no tests or a specific test.
To speed up your development process you can remove the `--tear-down-csi` flag to keep the manifests in place on the cluster.

