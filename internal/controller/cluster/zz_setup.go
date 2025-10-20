// SPDX-FileCopyrightText: 2024 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane/upjet/v2/pkg/controller"

	providerconfig "github.com/dana-team/provider-dns-v2/internal/controller/cluster/providerconfig"
	cnamerecord "github.com/dana-team/provider-dns-v2/internal/controller/cluster/record/cnamerecord"
	ptrrecord "github.com/dana-team/provider-dns-v2/internal/controller/cluster/record/ptrrecord"
	aaaarecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/aaaarecordset"
	arecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/arecordset"
	mxrecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/mxrecordset"
	nsrecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/nsrecordset"
	srvrecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/srvrecordset"
	txtrecordset "github.com/dana-team/provider-dns-v2/internal/controller/cluster/recordset/txtrecordset"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		providerconfig.Setup,
		cnamerecord.Setup,
		ptrrecord.Setup,
		aaaarecordset.Setup,
		arecordset.Setup,
		mxrecordset.Setup,
		nsrecordset.Setup,
		srvrecordset.Setup,
		txtrecordset.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupGated creates all controllers with the supplied logger and adds them to
// the supplied manager gated.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		providerconfig.SetupGated,
		cnamerecord.SetupGated,
		ptrrecord.SetupGated,
		aaaarecordset.SetupGated,
		arecordset.SetupGated,
		mxrecordset.SetupGated,
		nsrecordset.SetupGated,
		srvrecordset.SetupGated,
		txtrecordset.SetupGated,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}
