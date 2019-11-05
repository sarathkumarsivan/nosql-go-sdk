//
// Copyright (C) 2019 Oracle and/or its affiliates. All rights reserved.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl
//
// Please see LICENSE.txt file included in the top-level directory of the
// appropriate download for a copy of the license and additional information.
//

// A sample program that demonstrates how to perform basic operations on a Oracle NoSQL table.
package main

import (
	"fmt"
	"time"

	"github.com/oracle/nosql-go-sdk/examples"
	"github.com/oracle/nosql-go-sdk/nosqldb"
	"github.com/oracle/nosql-go-sdk/nosqldb/jsonutil"
	"github.com/oracle/nosql-go-sdk/nosqldb/types"
)

func main() {
	args := examples.ParseArgs()
	runBasicTableExample(args)
}

func runBasicTableExample(args *examples.Args) {

	authProvider, err := examples.CreateAuthorizationProvider(args)
	examples.ExitOnError(err)

	cfg := nosqldb.Config{
		Endpoint:              args.Endpoint,
		AuthorizationProvider: authProvider,
	}
	client, err := nosqldb.NewClient(cfg)
	examples.ExitOnError(err)

	// Creates a simple table with a LONG key and a single JSON field.
	tableName := "audienceData"
	stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s ("+
		"cookie_id LONG, "+
		"audience_data JSON, "+
		"PRIMARY KEY(cookie_id))",
		tableName)
	tableReq := &nosqldb.TableRequest{
		Statement: stmt,
		TableLimits: &nosqldb.TableLimits{
			ReadUnits:  50,
			WriteUnits: 50,
			StorageGB:  5,
		},
	}
	tableRes, err := client.DoTableRequest(tableReq)
	examples.ExitOnError(err)
	fmt.Println("Creating table", tableName)

	// The create table request is asynchronous, wait for table creation to complete.
	_, err = tableRes.WaitForCompletion(client, 60*time.Second, time.Second)
	examples.ExitOnError(err)
	fmt.Println("Created table", tableName)

	// Put a row
	//
	// Construct a simple row, specifying the values for each field.
	// The value for the row is this:
	//
	// {
	//   "cookie_id": 123,
	//   "audience_data": {
	//     "ipaddr": "10.0.0.3",
	//     "audience_segment": {
	//       "sports_lover": "2018-11-30",
	//       "book_reader": "2018-12-01"
	//     }
	//   }
	// }
	val := map[string]interface{}{
		"cookie_id": 123,
		"audience_data": map[string]interface{}{
			"ipaddr": "10.0.0.3",
			"audience_segment": map[string]interface{}{
				"sports_lover": "2018-11-30",
				"book_reader":  "2018-12-01",
			},
		},
	}
	putReq := &nosqldb.PutRequest{
		TableName: tableName,
		Value:     types.NewMapValue(val),
	}
	putRes, err := client.Put(putReq)
	examples.ExitOnError(err)
	fmt.Printf("Put row: %v\nresult: %v\n", jsonutil.AsPrettyJSON(putReq.Value.Map()), putRes)

	// Get the row
	key := &types.MapValue{}
	key.Put("cookie_id", 123)
	getReq := &nosqldb.GetRequest{
		TableName: tableName,
		Key:       key,
	}
	getRes, err := client.Get(getReq)
	examples.ExitOnError(err)
	fmt.Printf("Got row: %v\n", getRes.ValueAsJSON())

	// PUT a second row using JSON to enter the entire value
	jsonString := "{" +
		`"cookie_id": 456, ` +
		`"audience_data": {"ipaddr": "10.0.0.4", ` +
		`"audience_segment": {"sports_lover": "2019-01-05", "foodie": "2018-12-31"}}}`

	value, err := types.NewMapValueFromJSON(jsonString)
	examples.ExitOnError(err)

	putReq = &nosqldb.PutRequest{
		TableName: tableName,
		Value:     value,
	}
	putRes, err = client.Put(putReq)
	examples.ExitOnError(err)
	fmt.Printf("Put row from JSON: %v\n", jsonString)

	// Get the 2nd row
	key = &types.MapValue{}
	key.Put("cookie_id", 456)
	getReq = &nosqldb.GetRequest{
		TableName: tableName,
		Key:       key,
	}
	getRes, err = client.Get(getReq)
	examples.ExitOnError(err)
	fmt.Printf("Got second row: %v\n", getRes.ValueAsJSON())

	// QUERY the table. The table name is inferred from the query statement.
	query := "select * from " + tableName + " where cookie_id=123"
	results, err := examples.RunQuery(client, query)
	examples.ExitOnError(err)
	fmt.Printf("Number of query results for %q: %d\n", query, len(results))
	for i, r := range results {
		fmt.Printf("\t%d: %s\n", i+1, jsonutil.AsJSON(r.Map()))
	}

	// Delete a row
	delReq := &nosqldb.DeleteRequest{
		TableName: tableName,
		Key:       key,
	}
	delRes, err := client.Delete(delReq)
	examples.ExitOnError(err)
	fmt.Printf("Deleted key: %v\nresult: %v\n", jsonutil.AsJSON(delReq.Key.Map()), delRes)

	// Drop the table
	dropReq := &nosqldb.TableRequest{
		Statement: "DROP TABLE IF EXISTS " + tableName,
	}
	tableRes, err = client.DoTableRequestAndWait(dropReq, 60*time.Second, time.Second)
	fmt.Println("Dropped table", tableName)
}