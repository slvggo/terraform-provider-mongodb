package mongodb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/net/proxy"
)

type ClientConfig struct {
	Host                   string
	Port                   string
	Username               string
	Password               string
	DB                     string
	Ssl                    bool
	InsecureSkipVerify     bool
	ReplicaSet             string
	ReplicaSetHosts        string
	RetryWrites            bool
	Certificate            string
	Direct                 bool
	Proxy                  string
	Timeout                int
	ConnectTimeout         int
	ServerSelectionTimeout int
	ReadPreference         string
	MaxPoolSize            int
	MaxConnecting          int
}
type DbUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type Role struct {
	Role string `json:"role"`
	Db   string `json:"db"`
}

func (role Role) String() string {
	return fmt.Sprintf("{ role : %s , db : %s }", role.Role, role.Db)
}

type PrivilegeDto struct {
	Db         string   `json:"db"`
	Collection string   `json:"collection"`
	Actions    []string `json:"actions"`
}

type Privilege struct {
	Resource Resource `json:"resource"`
	Actions  []string `json:"actions"`
}
type SingleResultGetUser struct {
	Users []struct {
		Id    string `json:"_id"`
		User  string `json:"user"`
		Db    string `json:"db"`
		Roles []struct {
			Role string `json:"role"`
			Db   string `json:"db"`
		} `json:"roles"`
	} `json:"users"`
}
type SingleResultGetRole struct {
	Roles []struct {
		Role           string `json:"role"`
		Db             string `json:"db"`
		InheritedRoles []struct {
			Role string `json:"role"`
			Db   string `json:"db"`
		} `json:"inheritedRoles"`
		Privileges []struct {
			Resource struct {
				Db         string `json:"db"`
				Collection string `json:"collection"`
			} `json:"resource"`
			Actions []string `json:"actions"`
		} `json:"privileges"`
	} `json:"roles"`
}

type MongoProviderMeta struct {
	Config *ClientConfig
	Client *mongo.Client
}

func addArgs(arguments string, newArg string) string {
	if arguments != "" {
		return arguments + "&" + newArg
	} else {
		return "/?" + newArg
	}

}

func (c *ClientConfig) MongoClient() (*mongo.Client, error) {

	var verify = false
	var arguments = ""

	arguments = addArgs(arguments, "retrywrites="+strconv.FormatBool(c.RetryWrites))

	if c.Ssl {
		arguments = addArgs(arguments, "ssl=true")
	}

	if c.ReplicaSet != "" && c.Direct == false {
		arguments = addArgs(arguments, "replicaSet="+c.ReplicaSet)
	}

	if c.Direct {
		arguments = addArgs(arguments, "connect=direct")
	}

	arguments = addArgs(arguments, "timeoutMS="+strconv.Itoa(c.Timeout))
	arguments = addArgs(arguments, "connectTimeoutMS="+strconv.Itoa(c.ConnectTimeout))

	if c.ServerSelectionTimeout != 0 {
		arguments = addArgs(arguments, "serverSelectionTimeoutMS="+strconv.Itoa(c.ServerSelectionTimeout))
	}

	arguments = addArgs(arguments, "maxPoolSize="+strconv.Itoa(c.MaxPoolSize))
	arguments = addArgs(arguments, "maxConnecting="+strconv.Itoa(c.MaxConnecting))

	var uri string

	if c.ReplicaSetHosts != "" {
		arguments = addArgs(arguments, "readPreference="+c.ReadPreference)
		uri = "mongodb://" + c.ReplicaSetHosts + arguments
	} else {
		uri = "mongodb://" + c.Host + ":" + c.Port + arguments
	}

	dialer, dialerErr := proxyDialer(c)

	if dialerErr != nil {
		return nil, dialerErr
	}
	/*
		@Since: v0.0.9
		verify certificate
	*/
	if c.InsecureSkipVerify {
		verify = true
	}
	/*
		@Since: v0.0.7
		add certificate support for documentDB
	*/
	if c.Certificate != "" {
		tlsConfig, err := getTLSConfigWithAllServerCertificates([]byte(c.Certificate), verify)
		if err != nil {
			return nil, err
		}
		mongoClient, err := mongo.NewClient(options.Client().ApplyURI(uri).SetAuth(options.Credential{
			AuthSource: c.DB, Username: c.Username, Password: c.Password,
		}).SetTLSConfig(tlsConfig).SetDialer(dialer))

		return mongoClient, err
	}

	client, err := mongo.NewClient(options.Client().ApplyURI(uri).SetAuth(options.Credential{
		AuthSource: c.DB, Username: c.Username, Password: c.Password,
	}).SetDialer(dialer))
	return client, err
}

func getTLSConfigWithAllServerCertificates(ca []byte, verify bool) (*tls.Config, error) {
	/* As of version 1.2.1, the MongoDB Go Driver will only use the first CA server certificate found in sslcertificateauthorityfile.
	   The code below addresses this limitation by manually appending all server certificates found in sslcertificateauthorityfile
	   to a custom TLS configuration used during client creation. */

	tlsConfig := new(tls.Config)

	tlsConfig.InsecureSkipVerify = verify
	tlsConfig.RootCAs = x509.NewCertPool()
	ok := tlsConfig.RootCAs.AppendCertsFromPEM(ca)

	if !ok {
		return tlsConfig, errors.New("Failed parsing pem file")
	}

	return tlsConfig, nil
}

func (privilege Privilege) String() string {
	return fmt.Sprintf("{ resource : %s , actions : %s }", privilege.Resource, privilege.Actions)
}

type Resource struct {
	Db         string `json:"db"`
	Collection string `json:"collection"`
}

func (resource Resource) String() string {
	return fmt.Sprintf(" { db : %s , collection : %s }", resource.Db, resource.Collection)
}

func createUser(client *mongo.Client, user DbUser, roles []Role, authMechanisms []interface{}, database string) error {
	var result *mongo.SingleResult
	cmd := bson.D{
		{Key: "createUser", Value: user.Name},
	}

	if len(roles) > 0 {
		cmd = append(cmd, bson.E{Key: "roles", Value: roles})
	}

	if len(user.Password) != 0 {
		cmd = append(cmd, bson.E{Key: "pwd", Value: user.Password})
	}

	if len(authMechanisms) > 0 {
		cmd = append(cmd, bson.E{Key: "mechanisms", Value: authMechanisms})
	}

	result = client.Database(database).RunCommand(context.Background(), cmd)

	if result.Err() != nil {
		return result.Err()
	}
	return nil
}

func getUser(client *mongo.Client, username string, database string) (SingleResultGetUser, error) {
	var result *mongo.SingleResult
	result = client.Database(database).RunCommand(context.Background(), bson.D{{Key: "usersInfo", Value: bson.D{
		{Key: "user", Value: username},
		{Key: "db", Value: database},
	},
	}})
	var decodedResult SingleResultGetUser
	err := result.Decode(&decodedResult)
	if err != nil {
		return decodedResult, err
	}
	return decodedResult, nil
}

func getRole(client *mongo.Client, roleName string, database string) (SingleResultGetRole, error) {
	var result *mongo.SingleResult
	result = client.Database(database).RunCommand(context.Background(), bson.D{{Key: "rolesInfo", Value: bson.D{
		{Key: "role", Value: roleName},
		{Key: "db", Value: database},
	},
	},
		{Key: "showPrivileges", Value: true},
	})
	var decodedResult SingleResultGetRole
	err := result.Decode(&decodedResult)
	if err != nil {
		return decodedResult, err
	}
	return decodedResult, nil
}

func createRole(client *mongo.Client, role string, roles []Role, privilege []PrivilegeDto, database string) error {
	var result *mongo.SingleResult

	command := getRoleManagementCommand("createRole", role, roles, privilege)

	result = client.Database(database).RunCommand(context.Background(), command)

	if result.Err() != nil {
		return result.Err()
	}
	return nil
}

func updateRole(client *mongo.Client, role string, roles []Role, privilege []PrivilegeDto, database string) error {

	var result *mongo.SingleResult

	command := getRoleManagementCommand("updateRole", role, roles, privilege)

	result = client.Database(database).RunCommand(context.Background(), command)

	if result.Err() != nil {
		return result.Err()
	}
	return nil

}

func MongoClientInit(conf *ClientConfig) (*mongo.Client, error) {

	client, err := conf.MongoClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(conf.Timeout+conf.ConnectTimeout)*time.Millisecond)
	defer cancel()
	err = client.Connect(ctx)
	if err != nil {
		return nil, err
	}
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func proxyDialer(c *ClientConfig) (options.ContextDialer, error) {
	proxyFromEnv := proxy.FromEnvironment().(options.ContextDialer)
	proxyFromProvider := c.Proxy

	if len(proxyFromProvider) > 0 {
		proxyURL, err := url.Parse(proxyFromProvider)
		if err != nil {
			return nil, err
		}
		proxyDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, err
		}

		return proxyDialer.(options.ContextDialer), nil
	}

	return proxyFromEnv, nil
}
