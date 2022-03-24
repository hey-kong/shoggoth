/*
Copyright 2021 The KubeEdge Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"fmt"
	"os"
	"path"

	"github.com/spf13/cobra"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/klog/v2"

	"metaedge/cmd/metaedge-edge/app/options"
	"metaedge/pkg/edge/common"
	"metaedge/pkg/edge/controller"
	"metaedge/pkg/edge/controller/dataset"
	"metaedge/pkg/edge/handler"
	"metaedge/pkg/edge/server"
	"metaedge/pkg/version/verflag"
)

var (
	// Options defines the lc options
	Options *options.LocalControllerOptions
)

// NewLocalControllerCommand creates a command object
func NewLocalControllerCommand() *cobra.Command {
	cmdName := path.Base(os.Args[0])
	cmd := &cobra.Command{
		Use: cmdName,
		Long: fmt.Sprintf(`%s is the localcontroller.
It manages dataset and models, and controls ai features in local nodes.`, cmdName),
		Run: func(cmd *cobra.Command, args []string) {
			runServer()
		},
	}

	fs := cmd.Flags()
	namedFs := cliflag.NamedFlagSets{}

	verflag.AddFlags(namedFs.FlagSet("global"))
	globalflag.AddGlobalFlags(namedFs.FlagSet("global"), cmd.Name())
	for _, f := range namedFs.FlagSets {
		fs.AddFlagSet(f)
	}

	Options = options.NewLocalControllerOptions()

	Options.CloudAddr = os.Getenv(common.CloudAddressENV)
	if Options.NodeName = os.Getenv(common.NodeNameENV); Options.NodeName == "" {
		Options.NodeName = os.Getenv(common.HostNameENV)
	}

	var ok bool
	if Options.VolumeMountPrefix, ok = os.LookupEnv(common.RootFSMountDirENV); !ok {
		Options.VolumeMountPrefix = "/rootfs"
	}

	if Options.BindPort = os.Getenv(common.BindPortENV); Options.BindPort == "" {
		Options.BindPort = "9100"
	}

	return cmd
}

// runServer runs server
func runServer() {
	c := handler.NewWebSocketClient(Options)
	if err := c.Start(); err != nil {
		return
	}

	dm := dataset.New(c, Options)

	// mm := model.New(c)

	// fm := federatedlearning.New(c)

	// im := incrementallearning.New(c, dm, mm, Options)

	// lm := lifelonglearning.New(c, dm, Options)

	s := server.New(Options)

	for _, m := range []controller.FeatureManager{
		dm,
		// dm, mm, fm, im, lm,
	} {
		s.AddFeatureManager(m)
		c.Subscribe(m)
		err := m.Start()
		if err != nil {
			klog.Errorf("failed to start manager %s: %v",
				m.GetName(), err)
			return
		}
		klog.Infof("manager %s is started", m.GetName())
	}

	s.ListenAndServe()
}
