/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import "fmt"

// Business Error Code Definitions
// Range: 100000-999999
// 0 represents success

// IaaS Resource Related Error Codes (1xxxxx)
const (
	// common errors (1000xx)
	ErrUnknown              = 100000
	ErrInsufficientResource = 100001
	ErrResourceNotFound     = 100002
	ErrInvalidParameter     = 100003
	ErrPermissionDenied     = 100004
	ErrExecuteOnHyperFailed = 100005
	ErrOwnerNotFound        = 100006
	ErrEncryptionFailed     = 100008
	ErrJSONMarshalFailed    = 100009
	ErrResourcesInOrg       = 100010
	ErrInvalidCIDR          = 100011
	ErrCIDRTooBig           = 100012

	// database related errors (1001xx)
	ErrDatabaseError  = 100100
	ErrSQLSyntaxError = 100101

	// User, Organization, Member related errors (1002xx)
	ErrUserNotFound       = 100200
	ErrUserCreationFailed = 100201
	ErrUserUpdateFailed   = 100202
	ErrUserDeleteFailed   = 100203
	ErrOrgNotFound        = 100204
	ErrOrgCreationFailed  = 100205
	ErrOrgUpdateFailed    = 100206
	ErrOrgDeleteFailed    = 100207
	ErrNoRoleOnUser       = 100208
	ErrPasswordHashFailed = 100209
	ErrPasswordMismatch   = 100210

	// Member related errors (1003xx)
	ErrMemberNotFound       = 100300
	ErrMemberCreationFailed = 100301
	ErrMemberUpdateFailed   = 100302
	ErrMemberDeleteFailed   = 100303

	// Instance related errors (111xxx)
	ErrInstanceNotFound           = 111001
	ErrInstanceCreationFailed     = 111002
	ErrInstanceUpdateFailed       = 111003
	ErrInstanceDeleteFailed       = 111004
	ErrInstanceInvalidState       = 111005
	ErrInstanceInvalidConfig      = 111007
	ErrInstancePowerActionFail    = 111008
	ErrInstanceNoRouter           = 111009
	ErrInstanceNoPrimaryInterface = 111010
	ErrInvalidDomainFormat        = 111011
	ErrConsoleCreateFailed        = 111012
	ErrConsoleNotFound            = 111013
	ErrInvalidConsoleToken        = 111014
	ErrInvalidMetadata            = 111015

	// Flavor related errors (1119xx)
	ErrFlavorNotFound     = 111901
	ErrFlavorCreateFailed = 111902
	ErrFlavorUpdateFailed = 111903
	ErrFlavorDeleteFailed = 111904
	ErrFlavorInUse        = 111905
	ErrDiskTooSmall       = 111906

	// migration related errors (1118xx)
	ErrMigrationNotFound     = 111801
	ErrMigrationCreateFailed = 111802
	ErrMigrationUpdateFailed = 111803
	ErrMigrationDeleteFailed = 111804
	ErrMigrationInProgress   = 111805

	// Volume related errors (121xxx)
	ErrVolumeNotFound         = 121001
	ErrVolumeCreationFailed   = 121002
	ErrVolumeUpdateFailed     = 121003
	ErrVolumeDeleteFailed     = 121004
	ErrVolumeAttachFailed     = 121005
	ErrVolumeDetachFailed     = 121006
	ErrVolumeInvalidState     = 121007
	ErrVolumeInvalidSize      = 121008
	ErrBootVolumeNotFound     = 121009
	ErrBootVolumeUpdateFailed = 121010
	ErrBootVolumeDeleteFailed = 121011
	ErrVolumeIsInUse          = 121012
	ErrBootVolumeCannotDetach = 121013
	ErrVolumeIsBusy           = 121014

	// Snapshot/Backup related errors (1251xx)
	ErrBackupNotFound       = 125100
	ErrBackupCreationFailed = 125101
	ErrBackupUpdateFailed   = 125102
	ErrBackupDeleteFailed   = 125103
	ErrBackupInUse          = 125104

	// Network related errors (131xxx)
	// IP Address related errors (1310xx)
	ErrAddressNotFound     = 131001
	ErrAddressUpdateFailed = 131002
	ErrAddressDeleteFailed = 131003
	ErrInsufficientAddress = 131004
	ErrAddressCreateFailed = 131005
	ErrAddressInUse        = 131006

	// Subnet related errors (1311xx)
	ErrSubnetNotFound               = 131101
	ErrSubnetCreateFailed           = 131102
	ErrSubnetUpdateFailed           = 131103
	ErrSubnetDeleteFailed           = 131104
	ErrSubnetShouldBePublic         = 131105
	ErrSubnetShouldBeSite           = 131106
	ErrPublicSubnetNotFound         = 131107
	ErrSiteSubnetUpdateFailed       = 131108
	ErrSubnetsCrossVPCInOneInstance = 131109
	ErrPublicSubnetCannotInVPC      = 131110

	// Interface related errors (1312xx)
	ErrInterfaceNotFound             = 131201
	ErrInterfaceCreateFailed         = 131202
	ErrInterfaceUpdateFailed         = 131203
	ErrNotAllowInterfaceInSiteSubnet = 131204
	ErrInterfaceDeleteFailed         = 131205
	ErrCannotDeletePrimaryInterface  = 131206
	ErrTooManyInterfaces             = 131207
	ErrInterfaceInvalidSubnet        = 131208

	// Floating IP related errors (1312xx)
	ErrFIPNotFound               = 131201
	ErrFIPCreateFailed           = 131202
	ErrFIPUpdateFailed           = 131203
	ErrDeleteNativeFIPFailed     = 131204
	ErrUpdatePublicIPFailed      = 131205
	ErrUpdateInstIDOfFIPFailed   = 131206
	ErrUpdateSubnetIDOfFIPFailed = 131207
	ErrFIPDeleteFailed           = 131208
	ErrFIPInUse                  = 131209
	ErrDummyFIPCreateFailed      = 131210
	ErrUpdateGroupIDFailed       = 131211

	// VPC/Router related errors (1313xx)
	ErrRouterNotFound              = 131306
	ErrRouterCreateFailed          = 131307
	ErrRouterUpdateFailed          = 131308
	ErrRouterUpdateDefaultSGFailed = 131309
	ErrRouterDeleteFailed          = 131310
	ErrRouterInUse                 = 131311
	ErrRouterHasFloatingIPs        = 131312
	ErrRouterHasSubnets            = 131313
	ErrRouterHasPortmaps           = 131314

	// IP Group related errors (1314xx)
	ErrIpGroupNotFound     = 131401
	ErrIpGroupCreateFailed = 131402
	ErrIpGroupUpdateFailed = 131403
	ErrIpGroupDeleteFailed = 131404
	ErrIpGroupInUse        = 131405

	// Security related errors (141xxx)
	ErrSecurityGroupNotFound       = 141001
	ErrSecurityGroupCreateFailed   = 141002
	ErrSecurityGroupUpdateFailed   = 141003
	ErrSecurityGroupDeleteFailed   = 141004
	ErrAssociateSG2InterfaceFailed = 141005
	ErrAtLeastOneSGRequired        = 141006
	ErrCannotDeleteDefaultSG       = 141007
	ErrSGHasInterfaces             = 141008
	ErrSecurityRuleNotFound        = 141009
	ErrSecurityRuleInvalid         = 141010
	ErrSecurityRuleDeleteFailed    = 141011
	ErrSecurityRuleCreateFailed    = 141012
	ErrSecurityRuleUpdateFailed    = 141013

	// Image related errors (151xxx)
	ErrImageNotFound            = 151000
	ErrImageInUse               = 151001
	ErrImageNoQA                = 151002
	ErrImageCreateFailed        = 151003
	ErrImageUpdateFailed        = 151004
	ErrImageDeleteFailed        = 151005
	ErrImageNotAvailable        = 151006
	ErrImageStorageCreateFailed = 151007
	ErrImageStorageDeleteFailed = 151008
	ErrImageStorageUpdateFailed = 151009
	ErrImageStorageNotFound     = 151010
	ErrRescueImageNotFound      = 151011

	// ssh key related errors (161xxx)
	ErrSSHKeyNotFound       = 161001
	ErrSSHKeyCreateFailed   = 161002
	ErrSSHKeyUpdateFailed   = 161003
	ErrSSHKeyDeleteFailed   = 161004
	ErrSSHKeyGenerateFailed = 161005
	ErrSSHKeyInUse          = 161006

	// hypervisor/zone related errors (161xxx)
	ErrNoQualifiedHypervisor  = 161001
	ErrHypervisorNotFound     = 161002
	ErrHypervisorUpdateFailed = 161003
	ErrHypervisorDeleteFailed = 161004
	ErrHypervisorInvalidState = 161005
	ErrZoneNotFound           = 161006
	ErrUnsetDefaultZoneFailed = 161007
	ErrZoneCreationFailed     = 161008
	ErrZoneUpdateFailed       = 161009
	ErrZoneDeleteFailed       = 161010
	ErrHypersInZone           = 161011

	// dictionary related errors (1998xx)
	ErrDictionaryRecordsNotFound = 199801
	ErrDictionaryCreateFailed    = 199802
	ErrDictionaryUpdateFailed    = 199803
	ErrDictionaryDeleteFailed    = 199804
)

type CLError struct {
	Err     error
	Code    int
	Message string
}

func (e *CLError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("Code %d: %s (Details: %+v)", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("Code %d: %s", e.Code, e.Message)
}

func (e *CLError) Unwrap() error {
	return e.Err
}

func NewCLError(code int, message string, err error) *CLError {
	return &CLError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}
