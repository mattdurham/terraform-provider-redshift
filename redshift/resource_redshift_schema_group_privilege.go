package redshift

import (
	"github.com/hashicorp/terraform/helper/schema"
	"database/sql"
	"log"
	"time"
	"strings"
)

//https://docs.aws.amazon.com/redshift/latest/dg/r_GRANT.html
//https://docs.aws.amazon.com/redshift/latest/dg/r_REVOKE.html

/*




Permission model is limited:
	Grant
	Then do ALTER DEFAULT privileges


How to read:
	SELECT HAS_TABLE_PRIVILEGE('user1', 'table3', 'select');
 */

/*
TODO Id is schema_id || '_' || group_id, not sure if that is consistent for terraform --frankfarrell
 */
func redshiftSchemaGroupPrivilege() *schema.Resource {
	return &schema.Resource{
		Create: resourceRedshiftSchemaGroupPrivilegeCreate,
		Read:   resourceRedshiftSchemaGroupPrivilegeRead,
		Update: resourceRedshiftSchemaGroupPrivilegeUpdate,
		Delete: resourceRedshiftSchemaGroupPrivilegeDelete,
		Exists: resourceRedshiftSchemaGroupPrivilegeExists,
		Importer: &schema.ResourceImporter{
			State: resourceRedshiftSchemaGroupPrivilegeImport,
		},

		Schema: map[string]*schema.Schema{
			"schema_id": {
				Type:     schema.TypeInt,
				Required: true,
			},
			"group_id": {
				Type:     schema.TypeInt,
				Required: true,
			},
			"select": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"insert": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"update": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"delete": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"references": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func resourceRedshiftSchemaGroupPrivilegeExists(d *schema.ResourceData, meta interface{}) (bool, error) {
	// Exists - This is called to verify a resource still exists. It is called prior to Read,
	// and lowers the burden of Read to be able to assume the resource exists.
	client := meta.(*Client).db

	var aclId int

	err := client.QueryRow(`select acl.oid as aclid
		from pg_group pu, pg_default_acl acl, pg_namespace nsp
		where acl.defaclnamespace = nsp.oid and
		array_to_string(acl.defaclacl, '|') LIKE '%' || pu.groname || '=%'
		and nsp.oid || '_' || groupid = $1`,
		d.Id()).Scan(&aclId)


	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

func resourceRedshiftSchemaGroupPrivilegeCreate(d *schema.ResourceData, meta interface{}) error {

	redshiftClient := meta.(*Client).db

	tx, txErr := redshiftClient.Begin()

	if txErr != nil {
		panic(txErr)
	}


	var grants []string

	if v, ok := d.GetOk("select"); ok && v.(bool) {
		grants = append(grants, "SELECT")
	}
	if v, ok := d.GetOk("insert"); ok && v.(bool) {
		grants = append(grants, "INSERT")
	}
	if v, ok := d.GetOk("update"); ok && v.(bool) {
		grants = append(grants, "UPDATE")
	}
	if v, ok := d.GetOk("delete"); ok && v.(bool) {
		grants = append(grants, "DELETE")
	}
	if v, ok := d.GetOk("references"); ok && v.(bool) {
		grants = append(grants, "REFERENCES")
	}

	if(len(grants) == 0){
		tx.Rollback()
		return error("Must have at least 1 privilige")
	}

	schema_name, schemaErr := GetSchemanNameForSchemaId(tx, d.Get("schema_id").(int))
	if(schemaErr != nil){
		log.Fatal(schemaErr)
		tx.Rollback()
		return schemaErr
	}

	group_name, groupErr := GetGroupNameForGroupId(tx, d.Get("group_id").(int))
	if(groupErr != nil){
		log.Fatal(groupErr)
		tx.Rollback()
		return groupErr
	}

	var grant_privilege_statement = "GRANT " + strings.Join(grants[:],",") + " ON ALL TABLES IN SCHEMA " + schema_name + " TO GROUP " + group_name

	if _, err := tx.Exec(grant_privilege_statement); err != nil {
		log.Fatal(err)
		tx.Rollback()
		return err
	}

	var default_privileges_statement = "ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema_name +" GRANT "+ strings.Join(grants[:],",") + " ON TABLES TO GROUP " + group_name

	if _, err := tx.Exec(default_privileges_statement); err != nil {
		log.Fatal(err)
		tx.Rollback()
		return err
	}

	d.SetId(d.Get("schema_id").(int) + "_" + d.Get("group_id").(int))

	readErr := readRedshiftSchemaGroupPrivilege(d, tx)

	if readErr == nil {
		tx.Commit()
		return nil
	} else {
		tx.Rollback()
		return readErr
	}
}

func resourceRedshiftSchemaGroupPrivilegeRead(d *schema.ResourceData, meta interface{}) error {

	redshiftClient := meta.(*Client).db
	tx, txErr := redshiftClient.Begin()
	if txErr != nil {
		panic(txErr)
	}

	err := readRedshiftSchemaGroupPrivilege(d, tx)

	if err == nil {
		tx.Commit()
		return nil
	} else {
		tx.Rollback()
		return err
	}
}

func readRedshiftSchemaGroupPrivilege(d *schema.ResourceData, tx *sql.Tx) error {
	var (
		selectPrivilege  	bool
		updatePrivilege  	bool
		insertPrivilege  	bool
		deletePrivilege  	bool
		referencesPrivilege  	bool
	)

	var has_schema_privilege_query =
		`select decode(charindex('r',split_part(split_part(array_to_string(defaclacl, '|'),pu.groname,2 ) ,'/',1)),0,0,1)  as select,
			decode(charindex('w',split_part(split_part(array_to_string(defaclacl, '|'),pu.groname,2 ) ,'/',1)),0,0,1)  as update,
			decode(charindex('a',split_part(split_part(array_to_string(defaclacl, '|'),pu.groname,2 ) ,'/',1)),0,0,1)  as insert,
			decode(charindex('d',split_part(split_part(array_to_string(defaclacl, '|'),pu.groname,2 ) ,'/',1)),0,0,1)  as delete,
			decode(charindex('x',split_part(split_part(array_to_string(defaclacl, '|'),pu.groname,2 ) ,'/',1)),0,0,1)  as references
			from pg_group pu, pg_default_acl acl, pg_namespace nsp
			where acl.defaclnamespace = nsp.oid and
			array_to_string(acl.defaclacl, '|') LIKE '%' || pu.groname || '=%'
			and nsp.oid = $1
			and groupid = $2`

	schemaPrivilegesError := tx.QueryRow(has_schema_privilege_query, d.Get("schema_id").(int), d.Get("group_id").(int)).Scan(&selectPrivilege, &updatePrivilege, &insertPrivilege, &deletePrivilege, &referencesPrivilege)

	if schemaPrivilegesError != nil {
		tx.Rollback()
		return schemaPrivilegesError
	}

	d.Set("select", selectPrivilege)
	d.Set("insert", insertPrivilege)
	d.Set("update", updatePrivilege)
	d.Set("delete", deletePrivilege)
	d.Set("references", referencesPrivilege)

	return nil
}

func resourceRedshiftSchemaGroupPrivilegeUpdate(d *schema.ResourceData, meta interface{}) error {
	//TODO
}

func resourceRedshiftSchemaGroupPrivilegeDelete(d *schema.ResourceData, meta interface{}) error {
	//TODO
}

func resourceRedshiftSchemaGroupPrivilegeImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	if err := resourceRedshiftSchemaGroupPrivilegeRead(d, meta); err != nil {
		return nil, err
	}
	return []*schema.ResourceData{d}, nil
}
