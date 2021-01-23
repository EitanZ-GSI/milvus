// Copyright (C) 2019-2020 Zilliz. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License
// is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
// or implied. See the License for the specific language governing permissions and limitations under the License

#include <memory>

#include "SegmentInterface.h"
#include "common/LoadInfo.h"

namespace milvus::segcore {

class SegmentSealed : public SegmentInternalInterface {
 public:
    virtual void
    LoadIndex(const LoadIndexInfo& info) = 0;
    virtual void
    LoadFieldData(const LoadFieldDataInfo& info) = 0;
};

using SegmentSealedPtr = std::unique_ptr<SegmentSealed>;

SegmentSealedPtr
CreateSealedSegment(SchemaPtr schema, int64_t size_per_chunk = 32 * 1024);

}  // namespace milvus::segcore