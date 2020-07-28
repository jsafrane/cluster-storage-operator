package snapshotcrd

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type testContext struct {
	controller factory.Controller
	clients    *csoclients.Clients
}

type testObjects struct {
	// TODO: use CSO CRD
	storage *opv1.Storage
	crds    []*apiextv1.CustomResourceDefinition
}

type operatorTest struct {
	name            string
	initialObjects  testObjects
	expectedObjects testObjects
	expectErr       bool
}

func newController(test operatorTest) *testContext {
	// Convert to []runtime.Object
	initialObjects := &csoclients.FakeTestObjects{}
	for _, c := range test.initialObjects.crds {
		initialObjects.ExtensionObjects = append(initialObjects.ExtensionObjects, c)
	}
	if test.initialObjects.storage != nil {
		initialObjects.OperatorObjects = []runtime.Object{test.initialObjects.storage}
	}

	clients := csoclients.NewFakeClients(initialObjects)

	recorder := events.NewInMemoryRecorder("operator")
	ctrl := NewController(clients, recorder)

	return &testContext{
		controller: ctrl,
		clients:    clients,
	}
}

func getCRD(name string, versions ...string) *apiextv1.CustomResourceDefinition {
	var vers []apiextv1.CustomResourceDefinitionVersion
	for _, v := range versions {
		vers = append(vers, apiextv1.CustomResourceDefinitionVersion{
			Name: v,
		})
	}

	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Versions: vers,
		},
	}

	return crd
}

type crModifier func(cr *opv1.Storage) *opv1.Storage

func getCR(modifiers ...crModifier) *opv1.Storage {
	cr := &opv1.Storage{
		ObjectMeta: metav1.ObjectMeta{Name: operatorclient.GlobalConfigName},
		Spec: opv1.StorageSpec{
			OperatorSpec: opv1.OperatorSpec{
				ManagementState: opv1.Managed,
			},
		},
		Status: opv1.StorageStatus{},
	}
	for _, modifier := range modifiers {
		cr = modifier(cr)
	}
	return cr
}

func withTrueConditions(conditions ...string) crModifier {
	return func(i *opv1.Storage) *opv1.Storage {
		if i.Status.Conditions == nil {
			i.Status.Conditions = []opv1.OperatorCondition{}
		}
		for _, c := range conditions {
			i.Status.Conditions = append(i.Status.Conditions, opv1.OperatorCondition{
				Type:   c,
				Status: opv1.ConditionTrue,
			})
		}
		return i
	}
}

func withFalseConditions(conditions ...string) crModifier {
	return func(i *opv1.Storage) *opv1.Storage {
		if i.Status.Conditions == nil {
			i.Status.Conditions = []opv1.OperatorCondition{}
		}
		for _, c := range conditions {
			i.Status.Conditions = append(i.Status.Conditions, opv1.OperatorCondition{
				Type:   c,
				Status: opv1.ConditionFalse,
			})
		}
		return i
	}
}

func TestSync(t *testing.T) {
	tests := []operatorTest{
		{
			name: "no CRDs",
			initialObjects: testObjects{
				storage: getCR(),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(conditionsPrefix + opv1.OperatorStatusTypeUpgradeable),
				),
			},
			expectErr: false,
		},
		{
			name: "unrelated conditions are not overwritten",
			initialObjects: testObjects{
				storage: getCR(
					withTrueConditions("UnrelatedConditionAvailable"),
					withFalseConditions("AnotherConditionAvailable"),
				),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(
						"UnrelatedConditionAvailable",
						conditionsPrefix+opv1.OperatorStatusTypeUpgradeable,
					),
					withFalseConditions("AnotherConditionAvailable"),
				),
			},
			expectErr: false,
		},
		{
			name: "beta CRDs",
			initialObjects: testObjects{
				storage: getCR(),
				crds: []*apiextv1.CustomResourceDefinition{
					getCRD("volumesnapshots.snapshot.storage.k8s.io", "v1beta1"),
					getCRD("volumesnapshotcontents.snapshot.storage.k8s.io", "v1beta1"),
					getCRD("volumesnapshotclassess.snapshot.storage.k8s.io", "v1beta1"),
				},
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(conditionsPrefix + opv1.OperatorStatusTypeUpgradeable),
				),
			},
			expectErr: false,
		},
		{
			name: "alpha CRDs",
			initialObjects: testObjects{
				storage: getCR(),
				crds: []*apiextv1.CustomResourceDefinition{
					getCRD("volumesnapshots.snapshot.storage.k8s.io", "v1alpha1"),
					getCRD("volumesnapshotcontents.snapshot.storage.k8s.io", "v1alpha1"),
					getCRD("volumesnapshotclassess.snapshot.storage.k8s.io", "v1alpha1"),
				},
			},
			expectedObjects: testObjects{
				storage: getCR(
					withFalseConditions(conditionsPrefix + opv1.OperatorStatusTypeUpgradeable),
				),
			},
			expectErr: false,
		},
		{
			name: "mixed CRDs",
			initialObjects: testObjects{
				storage: getCR(),
				crds: []*apiextv1.CustomResourceDefinition{
					getCRD("volumesnapshots.snapshot.storage.k8s.io", "v1alpha1", "v1beta1"),
					getCRD("volumesnapshotcontents.snapshot.storage.k8s.io", "v1beta1"),
					getCRD("volumesnapshotclassess.snapshot.storage.k8s.io", "v1alpha1"),
				},
			},
			expectedObjects: testObjects{
				storage: getCR(
					withFalseConditions(conditionsPrefix + opv1.OperatorStatusTypeUpgradeable),
				),
			},
			expectErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Initialize
			ctx := newController(test)
			finish, cancel := context.WithCancel(context.TODO())
			defer cancel()
			csoclients.StartInformers(ctx.clients, finish.Done())
			csoclients.WaitForSync(ctx.clients, finish.Done())

			// Act
			err := ctx.controller.Sync(context.TODO(), nil)

			// Assert
			// Check error
			if err != nil && !test.expectErr {
				t.Errorf("sync() returned unexpected error: %v", err)
			}
			if err == nil && test.expectErr {
				t.Error("sync() unexpectedly succeeded when error was expected")
			}

			// Check expectedObjects.storage
			if test.expectedObjects.storage != nil {
				actualStorage, err := ctx.clients.OperatorClientSet.OperatorV1().Storages().Get(context.TODO(), "cluster", metav1.GetOptions{})
				if err != nil {
					t.Errorf("Failed to get Storage: %v", err)
				}
				sanitizeStorage(actualStorage)
				sanitizeStorage(test.expectedObjects.storage)
				if !equality.Semantic.DeepEqual(test.expectedObjects.storage, actualStorage) {
					t.Errorf("Unexpected Storage content:\n%s", cmp.Diff(test.expectedObjects.storage, actualStorage))
				}
			}
		})
	}
}

func sanitizeStorage(instance *opv1.Storage) {
	// Remove condition texts
	for i := range instance.Status.Conditions {
		instance.Status.Conditions[i].LastTransitionTime = metav1.Time{}
		instance.Status.Conditions[i].Message = ""
		instance.Status.Conditions[i].Reason = ""
	}
	// Sort the conditions by name to have consistent position in the array
	sort.Slice(instance.Status.Conditions, func(i, j int) bool {
		return instance.Status.Conditions[i].Type < instance.Status.Conditions[j].Type
	})
}
