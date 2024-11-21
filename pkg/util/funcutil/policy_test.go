package funcutil

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
)

func Test_GetPrivilegeExtObj(t *testing.T) {
	request := &milvuspb.LoadCollectionRequest{
		DbName:         "test",
		CollectionName: "col1",
	}
	privilegeExt, err := GetPrivilegeExtObj(request)
	assert.NoError(t, err)
	assert.Equal(t, commonpb.ObjectType_Collection, privilegeExt.ObjectType)
	assert.Equal(t, commonpb.ObjectPrivilege_PrivilegeLoad, privilegeExt.ObjectPrivilege)
	assert.Equal(t, int32(3), privilegeExt.ObjectNameIndex)

	request2 := &milvuspb.GetPersistentSegmentInfoRequest{}
	_, err = GetPrivilegeExtObj(request2)
	assert.Error(t, err)
}

func Test_GetResourceNames(t *testing.T) {
	request := &milvuspb.FlushRequest{
		DbName:          "test",
		CollectionNames: []string{"col1", "col2"},
	}
	assert.Equal(t, 0, len(GetObjectNames(request, 0)))
	assert.Equal(t, 0, len(GetObjectNames(request, 2)))
	names := GetObjectNames(request, 3)
	assert.Equal(t, 2, len(names))
}
