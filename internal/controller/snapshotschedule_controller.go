/*
Copyright 2024.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	"github.com/robfig/cron/v3"
	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sort"
	"time"

	batchv1 "crd.liuqhahah.com/custom-snap/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxRequeueTime     = 5 * time.Minute
	ScheduleKey        = "snapscheduler.backube/schedule"
	timeYYYYMMDDHHMMSS = "200601021504"
	WhenKey            = "snapscheduler.backube/when"
)

// SnapshotScheduleReconciler reconciles a SnapshotSchedule object
type SnapshotScheduleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=batch.crd.liuqhahah.com,resources=snapshotschedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch.crd.liuqhahah.com,resources=snapshotschedules/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch.crd.liuqhahah.com,resources=snapshotschedules/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SnapshotSchedule object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *SnapshotScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	reqLogger := log.FromContext(ctx).WithValues("SnapshotSchedule", req.NamespacedName)
	reqLogger.Info("Reconciling SnapshotSchedule")

	instance := &batchv1.SnapshotSchedule{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SnapshotScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.SnapshotSchedule{}).
		Complete(r)
}

func doReconcile(ctx context.Context, schedule *batchv1.SnapshotSchedule, logger logr.Logger, c client.Client) (ctrl.Result, error) {

	if schedule.Status.NextSnapshotTime.IsZero() {
		if err := updateNextSnapTime(schedule, time.Now()); err != nil {
			logger.Error(err, "unable to update next snapshot time")
			return ctrl.Result{}, err

		}
	}

	timeNow := time.Now()
	timeNext := schedule.Status.NextSnapshotTime.Time

	if !schedule.Spec.Disabled && timeNow.After(timeNext) {

		return handleSnapshotting(ctx, schedule, logger, c)
	}

	if err := updateNextSnapTime(schedule, timeNow); err != nil {
		logger.Error(err, "unable to update next snapshot time")
		return ctrl.Result{}, err

	}

	if err := expireByTime(ctx, schedule, timeNow, logger, c); err != nil {
		logger.Error(err, "unable to expire snapshots by time")
		return ctrl.Result{}, err

	}

	if err := expiredByCount(ctx, schedule, logger, c); err != nil {
		logger.Error(err, "unable to expire snapshots by count")
		return ctrl.Result{}, err
	}

	durTillNext := timeNext.Sub(timeNow)
	requeueTime := maxRequeueTime
	if durTillNext < requeueTime {
		requeueTime = durTillNext

	}
	return ctrl.Result{RequeueAfter: requeueTime}, nil
}

func expiredByCount(ctx context.Context, schedule *batchv1.SnapshotSchedule, logger logr.Logger, c client.Client) error {

	if schedule.Spec.Retention.MaxCount == nil {
		return nil
	}

	snapList, err := snapshotsFromSchedule(ctx, schedule, logger, c)
	if err != nil {
		logger.Error(err, "unable to retrieve list of snapshots")
		return err
	}

	grouped := groupSnapsByPVC(snapList)

	for _, list := range grouped {

		list = sortSnapsByTime(list)

		if len(list) > int(*schedule.Spec.Retention.MaxCount) {
			list = list[:len(list)-int(*schedule.Spec.Retention.MaxCount)]
			err := deleteSnapshots(ctx, list, logger, c)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func sortSnapsByTime(snaps []snapv1.VolumeSnapshot) []snapv1.VolumeSnapshot {
	sorted := append([]snapv1.VolumeSnapshot(nil), snaps...)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreationTimestamp.Before(&sorted[j].CreationTimestamp)
	})
	return sorted
}
func groupSnapsByPVC(snaps []snapv1.VolumeSnapshot) map[string][]snapv1.VolumeSnapshot {
	groupedSnaps := make(map[string][]snapv1.VolumeSnapshot)

	for _, snap := range snaps {
		pvcName := snap.Spec.Source.PersistentVolumeClaimName
		if pvcName != nil {
			if groupedSnaps[*pvcName] == nil {
				groupedSnaps[*pvcName] = []snapv1.VolumeSnapshot{}
			}
			groupedSnaps[*pvcName] = append(groupedSnaps[*pvcName], snap)
		}
	}

	return groupedSnaps

}
func getExpirationTime(schedule *batchv1.SnapshotSchedule,
	now time.Time, logger logr.Logger) (*time.Time, error) {
	if schedule.Spec.Retention.Expires == "" {
		return nil, nil
	}

	lifetime, err := time.ParseDuration(schedule.Spec.Retention.Expires)
	if err != nil {
		logger.Error(err, "unable to parse spec.retention.expires")
		return nil, err
	}

	if lifetime < 0 {
		err := errors.New("duration must be greater than 0")
		logger.Error(err, "invalid value for spec.retention.expires")
		return nil, err
	}

	expiration := now.Add(-lifetime).UTC()

	return &expiration, nil
}

func expireByTime(ctx context.Context, schedule *batchv1.SnapshotSchedule, now time.Time, logger logr.Logger, c client.Client) error {

	expiration, err := getExpirationTime(schedule, now, logger)
	if err != nil {
		logger.Error(err, "unable to determine snapshot expiration time")
		return err
	}

	if expiration == nil {
		return nil
	}

	snapList, err := snapshotsFromSchedule(ctx, schedule, logger, c)
	if err != nil {
		logger.Error(err, "unable to retrieve list of snapshots")
		return err

	}

	expiredSnaps := filterExpiredSnaps(snapList, *expiration)

	logger.Info("deleting expired snapshots", "expiration", expiration.Format(time.RFC3339),
		"total", len(snapList), "expired", len(expiredSnaps))

	err = deleteSnapshots(ctx, expiredSnaps, logger, c)
	return err

}

func deleteSnapshots(ctx context.Context, snapshots []snapv1.VolumeSnapshot,
	logger logr.Logger, c client.Client) error {
	for i := range snapshots {
		snap := snapshots[i]
		if err := c.Delete(ctx, &snap, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			logger.Error(err, "error deleting snapshot", "name", snap.Name)
			return err

		}
	}
	return nil
}

func filterExpiredSnaps(snaps []snapv1.VolumeSnapshot,
	expiration time.Time) []snapv1.VolumeSnapshot {
	outList := make([]snapv1.VolumeSnapshot, 0)
	for _, snap := range snaps {
		if snap.CreationTimestamp.Time.Before(expiration) {
			outList = append(outList, snap)
		}
	}
	return outList
}
func snapshotsFromSchedule(ctx context.Context, schedule *batchv1.SnapshotSchedule, logger logr.Logger, c client.Client) ([]snapv1.VolumeSnapshot, error) {
	labelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			ScheduleKey: schedule.Name,
		},
	}

	selector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		logger.Error(err, "unable to create label selector for snapshot expiration")
		return nil, err

	}

	listOpts := []client.ListOption{
		client.InNamespace(schedule.Namespace),
		client.MatchingLabelsSelector{
			Selector: selector,
		},
	}

	var snapList snapv1.VolumeSnapshotList

	err = c.List(ctx, &snapList, listOpts...)
	if err != nil {
		logger.Error(err, "unable to retrieve list of snapshots")
		return nil, err

	}

	return snapList.Items, nil
}

func updateNextSnapTime(snapshotSchedule *batchv1.SnapshotSchedule, reference time.Time) error {
	if snapshotSchedule == nil {
		return fmt.Errorf("nil snapshotSchedule instance")
	}

	next, err := getNextSnapTime(snapshotSchedule.Spec.Schedule, reference)
	if err != nil {
		snapshotSchedule.Status.NextSnapshotTime = nil
	} else {
		mv1time := metav1.NewTime(next)
		snapshotSchedule.Status.NextSnapshotTime = &mv1time
	}

	return err
}

func getNextSnapTime(cronSpec string, when time.Time) (time.Time, error) {

	schedule, err := parseCronSpec(cronSpec)
	if err != nil {
		return time.Time{}, err
	}

	next := schedule.Next(when)
	return next, nil
}

func parseCronSpec(cronSpec string) (cron.Schedule, error) {

	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return p.Parse(cronSpec)
}

func handleSnapshotting(ctx context.Context, schedule *batchv1.SnapshotSchedule, logger logr.Logger, c client.Client) (ctrl.Result, error) {

	pvcList, err := listPVCsMatchingSelector(ctx, logger, c, schedule.Namespace, &schedule.Spec.ClaimSelector)
	if err != nil {
		logger.Error(err, "unable to list PVCs")
		return ctrl.Result{}, err

	}

	snapTime := schedule.Status.NextSnapshotTime.Time.UTC()

	for _, pvc := range pvcList.Items {

		snapName := snapshotName(pvc.Name, schedule.Name, snapTime)
		logger.V(4).Info("snapshotName", "name", snapName)

		key := types.NamespacedName{
			Name:      snapName,
			Namespace: pvc.Namespace,
		}

		snap := snapv1.VolumeSnapshot{}

		if err := c.Get(ctx, key, &snap); err != nil {
			if kerrors.IsNotFound(err) {

				labels := make(map[string]string)
				var snapshotClassName *string
				if schedule.Spec.SnapshotTemplate != nil {
					labels = schedule.Spec.SnapshotTemplate.Labels
					snapshotClassName = schedule.Spec.SnapshotTemplate.SnapshotClassName

				}

				snap := newSnapForClaim(snapName, pvc, schedule.Name, snapTime, labels, snapshotClassName)
				if snap != nil {
					logger.Info("creating snapshot", "name", snapName)
					if err := c.Create(ctx, snap); err != nil {
						logger.Error(err, "unable to create snapshot", "name", snapName)
						return ctrl.Result{}, err
					}
				} else {
					logger.Info("snapshot creation disabled", "name", snapName)
				}

			} else {
				logger.Error(err, "unable to get snapshot", "name", snapName)
				return ctrl.Result{}, err
			}
		}
	}

	timeNow := metav1.Now()
	schedule.Status.LastSnapshotTime = &timeNow
	if err = updateNextSnapTime(schedule, timeNow.Time); err != nil {
		logger.Error(err, "unable to update next snapshot time")
		return ctrl.Result{}, err

	}
	return ctrl.Result{}, nil

}

func newSnapForClaim(snapName string, pvc v1.PersistentVolumeClaim, scheduleName string, scheduleTime time.Time,
	labels map[string]string, snapClass *string) *snapv1.VolumeSnapshot {

	numLabels := 2
	if labels != nil {
		numLabels += len(labels)
	}
	snapLabels := make(map[string]string, numLabels)
	for k, v := range labels {
		snapLabels[k] = v
	}

	snapLabels[ScheduleKey] = scheduleName
	snapLabels[WhenKey] = scheduleTime.Format(timeYYYYMMDDHHMMSS)
	return &snapv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapName,
			Namespace: pvc.Namespace,
			Labels:    snapLabels,
		},
		Spec: snapv1.VolumeSnapshotSpec{
			Source: snapv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvc.Name,
			},
			VolumeSnapshotClassName: snapClass,
		},
	}
}
func snapshotName(pvcName string, scheduleName string, time time.Time) string {
	nameBudget := validation.DNS1123SubdomainMaxLength - len(timeYYYYMMDDHHMMSS) - 2
	if len(pvcName)+len(scheduleName) > nameBudget {
		pvcOverBudget := len(pvcName) > nameBudget/2
		scheduleOverBudget := len(scheduleName) > nameBudget/2
		if pvcOverBudget && !scheduleOverBudget {
			pvcName = pvcName[0 : nameBudget-len(scheduleName)]
		}
		if !pvcOverBudget && scheduleOverBudget {
			scheduleName = scheduleName[0 : nameBudget-len(pvcName)]
		}

		if pvcOverBudget && scheduleOverBudget {
			scheduleName = scheduleName[0 : nameBudget/2]
			pvcName = pvcName[0 : nameBudget/2]
		}
	}
	return pvcName + "-" + scheduleName + "-" + time.Format(timeYYYYMMDDHHMMSS)
}

func listPVCsMatchingSelector(ctx context.Context, logger logr.Logger, c client.Client, namespace string, ls *metav1.LabelSelector) (*v1.PersistentVolumeClaimList, error) {
	selector, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, err
	}

	pvcList := &v1.PersistentVolumeClaimList{}
	listOps := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	}

	err = c.List(ctx, pvcList, listOps...)
	logger.Info("listPVCsMatchingSelector", "pvcList", pvcList)
	return pvcList, err
}
