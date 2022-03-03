package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/multierr"
	"go.viam.com/rplidar"

	"github.com/edaniels/golog"
	"go.viam.com/rdk/config"
	"go.viam.com/rdk/metadata/service"
	"go.viam.com/rdk/rlog"
	"go.viam.com/rdk/usb"
	"go.viam.com/utils"

	robotimpl "go.viam.com/rdk/robot/impl"
)

var (
	defaultTimeDeltaMilliseconds = 10
	defaultPort                  = 8081
	logger                       = rlog.Logger.Named("save_pcd_files")
	name                         = "rplidar"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

// Arguments for the command.
type Arguments struct {
	TimeDeltaMilliseconds int               `flag:"0"`
	Port                  utils.NetPortFlag `flag:"1"`
	DevicePath            string            `flag:"device,usage=device path"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) error {
	var argsParsed Arguments

	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}

	if argsParsed.TimeDeltaMilliseconds == 0 {
		logger.Debugf("using default time delta %d ", defaultTimeDeltaMilliseconds)
		argsParsed.TimeDeltaMilliseconds = defaultTimeDeltaMilliseconds
	} else {
		logger.Debugf("using user defined time delta %d ", argsParsed.TimeDeltaMilliseconds)
	}

	if argsParsed.Port == 0 {
		logger.Debugf("using default port %d ", defaultPort)
		argsParsed.Port = utils.NetPortFlag(defaultPort)
	} else {
		logger.Debugf("using user defined port %d ", argsParsed.Port)
	}

	usbDevices := usb.Search(
		usb.SearchFilter{},
		func(vendorID, productID int) bool {
			return vendorID == rplidar.USBInfo.Vendor && productID == rplidar.USBInfo.Product
		})

	if len(usbDevices) != 0 {
		logger.Debugf("detected %d lidar devices", len(usbDevices))
		for _, comp := range usbDevices {
			logger.Debug(comp)
		}
	} else {
		return errors.New("no usb devices found")
	}

	// Create rplidar component
	lidarDevice := config.Component{
		Name:       name,
		Type:       config.ComponentTypeCamera,
		Model:      rplidar.ModelName,
		Attributes: config.AttributeMap{"device_path": usbDevices[0].Path},
	}

	// Create new data directory
	newpath := filepath.Join(".", "data")

	err := os.RemoveAll(newpath)
	if err != nil {
		return errors.New("error deleting data directory")
	}

	err = os.MkdirAll(newpath, 0777)
	if err != nil {
		return errors.New("error creating data directory")
	}

	return savePCDFiles(ctx, argsParsed.TimeDeltaMilliseconds, int(argsParsed.Port), lidarDevice, logger)
}

func savePCDFiles(ctx context.Context, timeDeltaMilliseconds int, port int, lidarComponent config.Component, logger golog.Logger) (err error) {

	metadataSvc, err := service.New()
	if err != nil {
		return err
	}
	ctx = service.ContextWithService(ctx, metadataSvc)

	cfg := &config.Config{Components: []config.Component{lidarComponent}}
	myRobot, err := robotimpl.New(ctx, cfg, logger)
	if err != nil {
		return err
	}

	rplidar, ok := myRobot.CameraByName(name)
	if !ok {
		return errors.New("no rplidar found with name: " + name)
	}

	// Wait one second to allow rplidar to finish initializing
	if !utils.SelectContextOrWait(ctx, time.Second) {
		return multierr.Combine(ctx.Err(), myRobot.Close(ctx))
	}

	// Run loop
	for {
		if !utils.SelectContextOrWait(ctx, time.Duration(timeDeltaMilliseconds)*time.Millisecond) {
			return multierr.Combine(ctx.Err(), myRobot.Close(ctx))
		}

		pc, err := rplidar.NextPointCloud(ctx)
		if err != nil {
			return multierr.Combine(err, myRobot.Close(ctx))
		}
		logger.Infow("scanned", "pointcloud_size", pc.Size())
	}
}