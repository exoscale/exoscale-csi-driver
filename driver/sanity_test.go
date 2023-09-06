package driver

// TODO make all sanity tests pass
// func removeAll(path string) error {
// 	return os.RemoveAll(path)
// }
//
// func TestSanityCSI(t *testing.T) {
// 	driver := &Driver{
// 		// Fake helper implementing the API.
// 	}

// 	go func() {
// 		err := driver.Run()
// 		if err != nil {
// 			t.Error(err)
// 		}
// 	}()

// 	config := sanity.NewTestConfig()
// 	config.Address = "unix://endpoint"
// 	config.TestNodeVolumeAttachLimit = true
// 	config.TestVolumeExpandSize = config.TestVolumeSize * 2
// 	config.RemoveTargetPath = removeAll
// 	config.RemoveStagingPath = removeAll
// 	driver.config = &DriverConfig{
// 		Endpoint: config.Address,
// 	}
// 	sanity.Test(t, config)
// 	driver.srv.GracefulStop()
// 	os.RemoveAll("unix://endpoint")
// }

// TODO Implement fake helper methodes.
