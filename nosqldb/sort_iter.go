//
// Copyright (C) 2019, 2020 Oracle and/or its affiliates. All rights reserved.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl
//
// Please see LICENSE.txt file included in the top-level directory of the
// appropriate download for a copy of the license and additional information.
//

package nosqldb

import (
	"fmt"
	"sort"
	"strings"

	"github.com/oracle/nosql-go-sdk/nosqldb/internal/proto"
	"github.com/oracle/nosql-go-sdk/nosqldb/types"
)

var _ planIter = (*sortIter)(nil)

// sortSpec specifies criterias for sorting the values.
//
// The order-by clause, for each sort expression allows for an optional "sort spec",
// which specifies the relative order of NULLs (less than or greater than all other values)
// and whether the values returned by the sort expression should be sorted in ascending or descending order.
type sortSpec struct {
	// isDesc specifies if the desired sorting order is in descending order.
	isDesc bool

	// nullsFirst specifies if NULL values should sort before all other values.
	nullsFirst bool
}

func newSortSpec(r proto.Reader) (sp *sortSpec, err error) {
	isDesc, err := r.ReadBoolean()
	if err != nil {
		return
	}

	nullsFirst, err := r.ReadBoolean()
	if err != nil {
		return
	}

	sp = &sortSpec{
		isDesc:     isDesc,
		nullsFirst: nullsFirst,
	}
	return
}

// sortIterState represents the dynamic state for a sort iterator.
type sortIterState struct {
	*iterState
	results           []*types.MapValue
	nextResultPos     int
	memoryConsumption int64
}

func newSortIterState() *sortIterState {
	state := &sortIterState{
		iterState: newIterState(),
		results:   make([]*types.MapValue, 0, 100),
	}
	// The memory overhead for sortIterState object is counted.
	state.memoryConsumption = int64(sizeOf(state))
	return state
}

func (st *sortIterState) close() (err error) {
	if err = st.iterState.close(); err != nil {
		return
	}

	st.results = nil
	st.nextResultPos = 0
	return
}

func (st *sortIterState) done() (err error) {
	if err = st.iterState.done(); err != nil {
		return
	}

	st.results = nil
	st.nextResultPos = 0
	return
}

func (st *sortIterState) reset() (err error) {
	if err = st.iterState.reset(); err != nil {
		return
	}

	st.results = st.results[:0]
	st.nextResultPos = 0
	st.memoryConsumption = int64(sizeOf(st))
	return nil
}

// sortIter represents a plan iterator that sorts query results based on their
// values on a specified set of top-level fields.
//
// This is used to implement the geo_near function, which sorts results by distance.
type sortIter struct {
	*planIterDelegate

	// The plan iterator for input values.
	input planIter

	// sortFields specifies the names of top-level fields that contain the
	// values on which to sort the received results.
	sortFields []string

	// sortSpecs represents the corresponding sorting specs of the fields
	// specified in sortFields.
	sortSpecs []*sortSpec
}

func newSortIter(r proto.Reader) (iter *sortIter, err error) {
	delegate, err := newPlanIterDelegate(r, sorting)
	if err != nil {
		return
	}

	input, err := deserializePlanIter(r)
	if err != nil {
		return
	}

	sortFields, err := readStringArray(r)
	if err != nil {
		return
	}

	sortSpecs, err := readSortSpecs(r)
	if err != nil {
		return
	}

	iter = &sortIter{
		planIterDelegate: delegate,
		input:            input,
		sortFields:       sortFields,
		sortSpecs:        sortSpecs,
	}
	return
}

func (iter *sortIter) open(rcb *runtimeControlBlock) (err error) {
	state := newSortIterState()
	rcb.setState(iter.statePos, state)
	err = rcb.incMemoryConsumption(state.memoryConsumption)
	if err != nil {
		return
	}
	return iter.input.open(rcb)
}

func (iter *sortIter) reset(rcb *runtimeControlBlock) (err error) {
	if err = iter.input.reset(rcb); err != nil {
		return
	}

	st := rcb.getState(iter.statePos)
	state, ok := st.(*sortIterState)
	if !ok {
		return fmt.Errorf("wrong iterator state type, expect *sortIterState, got %T", st)
	}

	m1 := state.memoryConsumption
	err = state.reset()
	m2 := state.memoryConsumption
	// (m1 - m2) represents the memory consumed for the sorting query without
	// the sortIterState overhead itself.
	rcb.decMemoryConsumption(m1 - m2)
	return
}

func (iter *sortIter) close(rcb *runtimeControlBlock) (err error) {
	state := rcb.getState(iter.statePos)
	if state == nil {
		return
	}

	if err = iter.input.close(rcb); err != nil {
		return
	}

	return state.close()
}

func (iter *sortIter) next(rcb *runtimeControlBlock) (more bool, err error) {
	var ok bool
	st := rcb.getState(iter.statePos)
	state, ok := st.(*sortIterState)
	if !ok {
		return false, fmt.Errorf("wrong iterator state type, expect *sortIterState, got %T", st)
	}

	if state.isDone() {
		return false, nil
	}

	var v *types.MapValue
	var sz int64

	if state.isOpen() {

		for {
			more, err = iter.input.next(rcb)
			if err != nil {
				return false, err
			}

			if !more {
				break
			}

			res := iter.input.getResult(rcb)
			v, ok = res.(*types.MapValue)
			if !ok {
				return false, fmt.Errorf("the value should be a *types.MapValue, got %T", res)
			}

			state.results = append(state.results, v)

			sz = int64(sizeOf(v))
			state.memoryConsumption += sz
			err = rcb.incMemoryConsumption(sz)
			if err != nil {
				return false, err
			}
		}

		if rcb.reachedLimit {
			return false, nil
		}

		iter.sortResults(state.results)
		state.setState(running)
	}

	if state.nextResultPos < len(state.results) {
		rcb.setRegValue(iter.resultReg, state.results[state.nextResultPos])
		state.nextResultPos++
		return true, nil
	}

	state.done()
	return false, nil
}

func (iter *sortIter) sortResults(res []*types.MapValue) {
	if len(res) < 2 {
		return
	}

	by := &resultsBySortSpec{
		sortFields: iter.sortFields,
		sortSpecs:  iter.sortSpecs,
		results:    res,
	}
	sort.Sort(by)
}

func (iter *sortIter) getPlan() string {
	return iter.planIterDelegate.getExecPlan(iter)
}

func (iter *sortIter) displayContent(sb *strings.Builder, f *planFormatter) {
	iter.planIterDelegate.displayPlan(iter.input, sb, f)
	f.printIndent(sb)
	sb.WriteString("Sort Fields: ")
	for i, fieldName := range iter.sortFields {
		sb.WriteString(fieldName)
		if i < len(iter.sortFields)-1 {
			sb.WriteString(", ")
		}
	}
	sb.WriteString(",\n")
}

// resultsBySortSpec is used to sort query results on the specified fields by the specified sortSpec.
//
// It implements the sort.Interface.
type resultsBySortSpec struct {
	results    []*types.MapValue
	sortFields []string
	sortSpecs  []*sortSpec
}

// Len returns the number of results.
func (r *resultsBySortSpec) Len() int {
	return len(r.results)
}

// Swap swaps the result with index i and j.
func (r *resultsBySortSpec) Swap(i, j int) {
	r.results[i], r.results[j] = r.results[j], r.results[i]
}

// Less reports whether the result with index i should sort before the one with index j.
func (r *resultsBySortSpec) Less(i, j int) bool {
	var isLess bool
	var k int
	var fieldName string

	for k, fieldName = range r.sortFields {
		v1, ok := r.results[i].Get(fieldName)
		if !ok {
			continue
		}

		v2, ok := r.results[j].Get(fieldName)
		if !ok {
			continue
		}

		sortSpec := r.sortSpecs[k]

		// The ordering of special values: EmptyValue, JSONNullValue, NullValue:
		//
		// If nullsFirst is specified and the direction is ASC/DESC, the special
		// values are considered less/greater than all non-special values.
		//
		// The relative ordering among the 3 special values themselves is fixed:
		// if the direction is ASC, the ordering is EmptyValue < JSONNullValue < NullValue
		// otherwise the ordering is reversed.
		if v1 == types.NullValueInstance {
			if v2 == types.NullValueInstance {
				continue
			}

			if v2 == types.EmptyValueInstance || v2 == types.JSONNullValueInstance {
				if sortSpec.isDesc {
					return true
				}
				return false
			}

			return sortSpec.nullsFirst
		}

		if v2 == types.NullValueInstance {
			if v1 == types.EmptyValueInstance || v1 == types.JSONNullValueInstance {
				if sortSpec.isDesc {
					return false
				}
				return true
			}

			return !sortSpec.nullsFirst
		}

		if v1 == types.EmptyValueInstance {
			if v2 == types.EmptyValueInstance {
				continue
			}
			if v2 == types.JSONNullValueInstance {
				if sortSpec.isDesc {
					return false
				}
				return true
			}

			return sortSpec.nullsFirst
		}

		if v2 == types.EmptyValueInstance {
			if v1 == types.JSONNullValueInstance {
				if sortSpec.isDesc {
					return true
				}
				return false
			}

			return !sortSpec.nullsFirst
		}

		if v1 == types.JSONNullValueInstance {
			if v2 == types.JSONNullValueInstance {
				continue
			}

			return sortSpec.nullsFirst
		}

		if v2 == types.JSONNullValueInstance {
			return !sortSpec.nullsFirst
		}

		compareRes, _ := compareAtomicValues(nil, true, v1, v2)
		if compareRes.comp == 0 {
			continue
		}

		isLess = compareRes.comp == -1

		if sortSpec.isDesc {
			return !isLess
		}
		return isLess
	}

	return false
}
