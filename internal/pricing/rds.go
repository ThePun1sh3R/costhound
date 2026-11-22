package pricing

import (
	"strconv"
	"strings"
)

// rdsTwins maps each previous-generation RDS family (Current Generation =
// No in AWS's AmazonRDS offer file) to the current-generation family of the
// same kind it is compared with: x86 first, then Graviton.
var rdsTwins = map[string][2]string{
	"db.t1": {"db.t3", "db.t4g"}, "db.t2": {"db.t3", "db.t4g"},
	"db.m1": {"db.m5", "db.m6g"}, "db.m3": {"db.m5", "db.m6g"}, "db.m4": {"db.m5", "db.m6g"},
	"db.m2": {"db.r5", "db.r6g"}, "db.r3": {"db.r5", "db.r6g"}, "db.r4": {"db.r5", "db.r6g"},
}

// rdsHour is the On-Demand price per hour {Single-AZ, Multi-AZ} of the
// previous-generation classes and their twins, and of the current x86
// classes and their Graviton twins, for the engines with no license fee. Source: AWS's public offer file AmazonRDS/current/us-east-1/
// index.csv, downloaded 2026-11-19 (Location Type "AWS Region", License
// Model "No license required"). Multi-AZ is 0 where AWS lists none.
var rdsHour = map[string][2]float64{ // "engine|class"
	"mariadb|db.m3.2xlarge":    {0.74, 1.48},
	"mariadb|db.m3.large":      {0.185, 0.37},
	"mariadb|db.m3.medium":     {0.09, 0.18},
	"mariadb|db.m3.xlarge":     {0.37, 0.74},
	"mariadb|db.m4.10xlarge":   {3.502, 7.004},
	"mariadb|db.m4.16xlarge":   {5.6, 11.2},
	"mariadb|db.m4.2xlarge":    {0.7, 1.4},
	"mariadb|db.m4.4xlarge":    {1.401, 2.802},
	"mariadb|db.m4.large":      {0.175, 0.35},
	"mariadb|db.m4.xlarge":     {0.35, 0.7},
	"mariadb|db.m5.12xlarge":   {4.104, 8.208},
	"mariadb|db.m5.16xlarge":   {5.47, 10.94},
	"mariadb|db.m5.24xlarge":   {8.208, 16.416},
	"mariadb|db.m5.2xlarge":    {0.684, 1.368},
	"mariadb|db.m5.4xlarge":    {1.368, 2.736},
	"mariadb|db.m5.8xlarge":    {2.74, 5.47},
	"mariadb|db.m5.large":      {0.171, 0.342},
	"mariadb|db.m5.xlarge":     {0.342, 0.684},
	"mariadb|db.m6g.12xlarge":  {3.648, 7.296},
	"mariadb|db.m6g.16xlarge":  {4.864, 9.728},
	"mariadb|db.m6g.2xlarge":   {0.608, 1.216},
	"mariadb|db.m6g.4xlarge":   {1.216, 2.432},
	"mariadb|db.m6g.8xlarge":   {2.432, 4.864},
	"mariadb|db.m6g.large":     {0.152, 0.304},
	"mariadb|db.m6g.xlarge":    {0.304, 0.608},
	"mariadb|db.m6i.12xlarge":  {4.104, 8.208},
	"mariadb|db.m6i.16xlarge":  {5.472, 10.944},
	"mariadb|db.m6i.24xlarge":  {8.208, 16.416},
	"mariadb|db.m6i.2xlarge":   {0.684, 1.368},
	"mariadb|db.m6i.32xlarge":  {10.944, 21.888},
	"mariadb|db.m6i.4xlarge":   {1.368, 2.736},
	"mariadb|db.m6i.8xlarge":   {2.736, 5.472},
	"mariadb|db.m6i.large":     {0.171, 0.342},
	"mariadb|db.m6i.xlarge":    {0.342, 0.684},
	"mariadb|db.m7g.12xlarge":  {4.044, 8.088},
	"mariadb|db.m7g.16xlarge":  {5.392, 10.784},
	"mariadb|db.m7g.2xlarge":   {0.674, 1.348},
	"mariadb|db.m7g.4xlarge":   {1.348, 2.696},
	"mariadb|db.m7g.8xlarge":   {2.696, 5.392},
	"mariadb|db.m7g.large":     {0.168, 0.337},
	"mariadb|db.m7g.xlarge":    {0.337, 0.674},
	"mariadb|db.m7i.12xlarge":  {4.272, 8.544},
	"mariadb|db.m7i.16xlarge":  {5.696, 11.392},
	"mariadb|db.m7i.24xlarge":  {8.544, 17.088},
	"mariadb|db.m7i.2xlarge":   {0.712, 1.424},
	"mariadb|db.m7i.48xlarge":  {17.088, 34.176},
	"mariadb|db.m7i.4xlarge":   {1.424, 2.848},
	"mariadb|db.m7i.8xlarge":   {2.848, 5.696},
	"mariadb|db.m7i.large":     {0.178, 0.356},
	"mariadb|db.m7i.xlarge":    {0.356, 0.712},
	"mariadb|db.r3.2xlarge":    {0.945, 1.89},
	"mariadb|db.r3.4xlarge":    {1.89, 3.78},
	"mariadb|db.r3.8xlarge":    {3.78, 7.56},
	"mariadb|db.r3.large":      {0.24, 0.48},
	"mariadb|db.r3.xlarge":     {0.475, 0.95},
	"mariadb|db.r4.16xlarge":   {7.68, 15.36},
	"mariadb|db.r4.2xlarge":    {0.96, 1.92},
	"mariadb|db.r4.4xlarge":    {1.92, 3.84},
	"mariadb|db.r4.8xlarge":    {3.84, 7.68},
	"mariadb|db.r4.large":      {0.24, 0.48},
	"mariadb|db.r4.xlarge":     {0.48, 0.96},
	"mariadb|db.r5.12xlarge":   {5.76, 11.52},
	"mariadb|db.r5.16xlarge":   {7.68, 15.36},
	"mariadb|db.r5.24xlarge":   {11.52, 23.04},
	"mariadb|db.r5.2xlarge":    {0.96, 1.92},
	"mariadb|db.r5.4xlarge":    {1.92, 3.84},
	"mariadb|db.r5.8xlarge":    {3.84, 7.68},
	"mariadb|db.r5.large":      {0.24, 0.48},
	"mariadb|db.r5.xlarge":     {0.48, 0.96},
	"mariadb|db.r6g.12xlarge":  {5.155, 10.31},
	"mariadb|db.r6g.16xlarge":  {6.874, 13.747},
	"mariadb|db.r6g.2xlarge":   {0.859, 1.718},
	"mariadb|db.r6g.4xlarge":   {1.718, 3.437},
	"mariadb|db.r6g.8xlarge":   {3.437, 6.874},
	"mariadb|db.r6g.large":     {0.215, 0.43},
	"mariadb|db.r6g.xlarge":    {0.43, 0.859},
	"mariadb|db.r6i.12xlarge":  {5.76, 11.52},
	"mariadb|db.r6i.16xlarge":  {7.68, 15.36},
	"mariadb|db.r6i.24xlarge":  {11.52, 23.04},
	"mariadb|db.r6i.2xlarge":   {0.96, 1.92},
	"mariadb|db.r6i.32xlarge":  {15.36, 30.72},
	"mariadb|db.r6i.4xlarge":   {1.92, 3.84},
	"mariadb|db.r6i.8xlarge":   {3.84, 7.68},
	"mariadb|db.r6i.large":     {0.24, 0.48},
	"mariadb|db.r6i.xlarge":    {0.48, 0.96},
	"mariadb|db.r7g.12xlarge":  {5.738, 11.475},
	"mariadb|db.r7g.16xlarge":  {7.65, 15.3},
	"mariadb|db.r7g.2xlarge":   {0.956, 1.913},
	"mariadb|db.r7g.4xlarge":   {1.913, 3.825},
	"mariadb|db.r7g.8xlarge":   {3.825, 7.65},
	"mariadb|db.r7g.large":     {0.239, 0.478},
	"mariadb|db.r7g.xlarge":    {0.478, 0.956},
	"mariadb|db.r7i.12xlarge":  {6, 12},
	"mariadb|db.r7i.16xlarge":  {8, 16},
	"mariadb|db.r7i.24xlarge":  {12, 24},
	"mariadb|db.r7i.2xlarge":   {1, 2},
	"mariadb|db.r7i.48xlarge":  {24, 48},
	"mariadb|db.r7i.4xlarge":   {2, 4},
	"mariadb|db.r7i.8xlarge":   {4, 8},
	"mariadb|db.r7i.large":     {0.25, 0.5},
	"mariadb|db.r7i.xlarge":    {0.5, 1},
	"mariadb|db.t2.2xlarge":    {0.544, 1.088},
	"mariadb|db.t2.large":      {0.136, 0.272},
	"mariadb|db.t2.medium":     {0.068, 0.136},
	"mariadb|db.t2.micro":      {0.017, 0.034},
	"mariadb|db.t2.small":      {0.034, 0.068},
	"mariadb|db.t2.xlarge":     {0.272, 0.544},
	"mariadb|db.t3.2xlarge":    {0.544, 1.088},
	"mariadb|db.t3.large":      {0.136, 0.272},
	"mariadb|db.t3.medium":     {0.068, 0.136},
	"mariadb|db.t3.micro":      {0.017, 0.034},
	"mariadb|db.t3.small":      {0.034, 0.068},
	"mariadb|db.t3.xlarge":     {0.272, 0.544},
	"mariadb|db.t4g.2xlarge":   {0.517, 1.034},
	"mariadb|db.t4g.large":     {0.129, 0.258},
	"mariadb|db.t4g.medium":    {0.065, 0.129},
	"mariadb|db.t4g.micro":     {0.016, 0.032},
	"mariadb|db.t4g.small":     {0.032, 0.065},
	"mariadb|db.t4g.xlarge":    {0.258, 0.517},
	"mysql|db.m1.large":        {0.23, 0.46},
	"mysql|db.m1.medium":       {0.115, 0.23},
	"mysql|db.m1.small":        {0.055, 0.11},
	"mysql|db.m1.xlarge":       {0.465, 0.93},
	"mysql|db.m2.2xlarge":      {0.66, 1.32},
	"mysql|db.m2.4xlarge":      {1.32, 2.64},
	"mysql|db.m2.xlarge":       {0.33, 0.66},
	"mysql|db.m3.2xlarge":      {0.74, 1.48},
	"mysql|db.m3.large":        {0.185, 0.37},
	"mysql|db.m3.medium":       {0.09, 0.18},
	"mysql|db.m3.xlarge":       {0.37, 0.74},
	"mysql|db.m4.10xlarge":     {3.502, 7.004},
	"mysql|db.m4.16xlarge":     {5.6, 11.2},
	"mysql|db.m4.2xlarge":      {0.7, 1.4},
	"mysql|db.m4.4xlarge":      {1.401, 2.802},
	"mysql|db.m4.large":        {0.175, 0.35},
	"mysql|db.m4.xlarge":       {0.35, 0.7},
	"mysql|db.m5.12xlarge":     {4.104, 8.208},
	"mysql|db.m5.16xlarge":     {5.47, 10.94},
	"mysql|db.m5.24xlarge":     {8.208, 16.416},
	"mysql|db.m5.2xlarge":      {0.684, 1.368},
	"mysql|db.m5.4xlarge":      {1.368, 2.736},
	"mysql|db.m5.8xlarge":      {2.74, 5.47},
	"mysql|db.m5.large":        {0.171, 0.342},
	"mysql|db.m5.xlarge":       {0.342, 0.684},
	"mysql|db.m6g.12xlarge":    {3.648, 7.296},
	"mysql|db.m6g.16xlarge":    {4.864, 9.728},
	"mysql|db.m6g.2xlarge":     {0.608, 1.216},
	"mysql|db.m6g.4xlarge":     {1.216, 2.432},
	"mysql|db.m6g.8xlarge":     {2.432, 4.864},
	"mysql|db.m6g.large":       {0.152, 0.304},
	"mysql|db.m6g.xlarge":      {0.304, 0.608},
	"mysql|db.m6i.12xlarge":    {4.104, 8.208},
	"mysql|db.m6i.16xlarge":    {5.472, 10.944},
	"mysql|db.m6i.24xlarge":    {8.208, 16.416},
	"mysql|db.m6i.2xlarge":     {0.684, 1.368},
	"mysql|db.m6i.32xlarge":    {10.944, 21.888},
	"mysql|db.m6i.4xlarge":     {1.368, 2.736},
	"mysql|db.m6i.8xlarge":     {2.736, 5.472},
	"mysql|db.m6i.large":       {0.171, 0.342},
	"mysql|db.m6i.xlarge":      {0.342, 0.684},
	"mysql|db.m7g.12xlarge":    {4.044, 8.088},
	"mysql|db.m7g.16xlarge":    {5.392, 10.784},
	"mysql|db.m7g.2xlarge":     {0.674, 1.348},
	"mysql|db.m7g.4xlarge":     {1.348, 2.696},
	"mysql|db.m7g.8xlarge":     {2.696, 5.392},
	"mysql|db.m7g.large":       {0.168, 0.337},
	"mysql|db.m7g.xlarge":      {0.337, 0.674},
	"mysql|db.m7i.12xlarge":    {4.272, 8.544},
	"mysql|db.m7i.16xlarge":    {5.696, 11.392},
	"mysql|db.m7i.24xlarge":    {8.544, 17.088},
	"mysql|db.m7i.2xlarge":     {0.712, 1.424},
	"mysql|db.m7i.48xlarge":    {17.088, 34.176},
	"mysql|db.m7i.4xlarge":     {1.424, 2.848},
	"mysql|db.m7i.8xlarge":     {2.848, 5.696},
	"mysql|db.m7i.large":       {0.178, 0.356},
	"mysql|db.m7i.xlarge":      {0.356, 0.712},
	"mysql|db.r3.2xlarge":      {0.945, 1.89},
	"mysql|db.r3.4xlarge":      {1.89, 3.78},
	"mysql|db.r3.8xlarge":      {3.78, 7.56},
	"mysql|db.r3.large":        {0.24, 0.48},
	"mysql|db.r3.xlarge":       {0.475, 0.95},
	"mysql|db.r4.16xlarge":     {7.68, 15.36},
	"mysql|db.r4.2xlarge":      {0.96, 1.92},
	"mysql|db.r4.4xlarge":      {1.92, 3.84},
	"mysql|db.r4.8xlarge":      {3.84, 7.68},
	"mysql|db.r4.large":        {0.24, 0.48},
	"mysql|db.r4.xlarge":       {0.48, 0.96},
	"mysql|db.r5.12xlarge":     {5.76, 11.52},
	"mysql|db.r5.16xlarge":     {7.68, 15.36},
	"mysql|db.r5.24xlarge":     {11.52, 23.04},
	"mysql|db.r5.2xlarge":      {0.96, 1.92},
	"mysql|db.r5.4xlarge":      {1.92, 3.84},
	"mysql|db.r5.8xlarge":      {3.84, 7.68},
	"mysql|db.r5.large":        {0.24, 0.48},
	"mysql|db.r5.xlarge":       {0.48, 0.96},
	"mysql|db.r6g.12xlarge":    {5.155, 10.31},
	"mysql|db.r6g.16xlarge":    {6.874, 13.747},
	"mysql|db.r6g.2xlarge":     {0.859, 1.718},
	"mysql|db.r6g.4xlarge":     {1.718, 3.437},
	"mysql|db.r6g.8xlarge":     {3.437, 6.874},
	"mysql|db.r6g.large":       {0.215, 0.43},
	"mysql|db.r6g.xlarge":      {0.43, 0.859},
	"mysql|db.r6i.12xlarge":    {5.76, 11.52},
	"mysql|db.r6i.16xlarge":    {7.68, 15.36},
	"mysql|db.r6i.24xlarge":    {11.52, 23.04},
	"mysql|db.r6i.2xlarge":     {0.96, 1.92},
	"mysql|db.r6i.32xlarge":    {15.36, 30.72},
	"mysql|db.r6i.4xlarge":     {1.92, 3.84},
	"mysql|db.r6i.8xlarge":     {3.84, 7.68},
	"mysql|db.r6i.large":       {0.24, 0.48},
	"mysql|db.r6i.xlarge":      {0.48, 0.96},
	"mysql|db.r7g.12xlarge":    {5.738, 11.475},
	"mysql|db.r7g.16xlarge":    {7.65, 15.3},
	"mysql|db.r7g.2xlarge":     {0.956, 1.913},
	"mysql|db.r7g.4xlarge":     {1.913, 3.825},
	"mysql|db.r7g.8xlarge":     {3.825, 7.65},
	"mysql|db.r7g.large":       {0.239, 0.478},
	"mysql|db.r7g.xlarge":      {0.478, 0.956},
	"mysql|db.r7i.12xlarge":    {6, 12},
	"mysql|db.r7i.16xlarge":    {8, 16},
	"mysql|db.r7i.24xlarge":    {12, 24},
	"mysql|db.r7i.2xlarge":     {1, 2},
	"mysql|db.r7i.48xlarge":    {24, 48},
	"mysql|db.r7i.4xlarge":     {2, 4},
	"mysql|db.r7i.8xlarge":     {4, 8},
	"mysql|db.r7i.large":       {0.25, 0.5},
	"mysql|db.r7i.xlarge":      {0.5, 1},
	"mysql|db.t1.micro":        {0.025, 0.05},
	"mysql|db.t2.2xlarge":      {0.544, 1.088},
	"mysql|db.t2.large":        {0.136, 0.272},
	"mysql|db.t2.medium":       {0.068, 0.136},
	"mysql|db.t2.micro":        {0.017, 0.034},
	"mysql|db.t2.small":        {0.034, 0.068},
	"mysql|db.t2.xlarge":       {0.272, 0.544},
	"mysql|db.t3.2xlarge":      {0.544, 1.088},
	"mysql|db.t3.large":        {0.136, 0.272},
	"mysql|db.t3.medium":       {0.068, 0.136},
	"mysql|db.t3.micro":        {0.017, 0.034},
	"mysql|db.t3.small":        {0.034, 0.068},
	"mysql|db.t3.xlarge":       {0.272, 0.544},
	"mysql|db.t4g.2xlarge":     {0.517, 1.034},
	"mysql|db.t4g.large":       {0.129, 0.258},
	"mysql|db.t4g.medium":      {0.065, 0.129},
	"mysql|db.t4g.micro":       {0.016, 0.032},
	"mysql|db.t4g.small":       {0.032, 0.065},
	"mysql|db.t4g.xlarge":      {0.258, 0.517},
	"postgres|db.m1.large":     {0.245, 0.49},
	"postgres|db.m1.medium":    {0.12, 0.24},
	"postgres|db.m1.small":     {0.06, 0.12},
	"postgres|db.m1.xlarge":    {0.49, 0.98},
	"postgres|db.m2.2xlarge":   {0.69, 1.38},
	"postgres|db.m2.4xlarge":   {1.39, 2.78},
	"postgres|db.m2.xlarge":    {0.345, 0.69},
	"postgres|db.m3.2xlarge":   {0.775, 1.55},
	"postgres|db.m3.large":     {0.195, 0.39},
	"postgres|db.m3.medium":    {0.095, 0.19},
	"postgres|db.m3.xlarge":    {0.39, 0.78},
	"postgres|db.m4.10xlarge":  {3.654, 7.308},
	"postgres|db.m4.16xlarge":  {5.844, 11.688},
	"postgres|db.m4.2xlarge":   {0.73, 1.46},
	"postgres|db.m4.4xlarge":   {1.461, 2.922},
	"postgres|db.m4.large":     {0.182, 0.364},
	"postgres|db.m4.xlarge":    {0.365, 0.73},
	"postgres|db.m5.12xlarge":  {4.272, 8.544},
	"postgres|db.m5.16xlarge":  {5.696, 11.392},
	"postgres|db.m5.24xlarge":  {8.544, 17.088},
	"postgres|db.m5.2xlarge":   {0.712, 1.424},
	"postgres|db.m5.4xlarge":   {1.424, 2.848},
	"postgres|db.m5.8xlarge":   {2.848, 5.696},
	"postgres|db.m5.large":     {0.178, 0.356},
	"postgres|db.m5.xlarge":    {0.356, 0.712},
	"postgres|db.m6g.12xlarge": {3.816, 7.632},
	"postgres|db.m6g.16xlarge": {5.088, 10.176},
	"postgres|db.m6g.2xlarge":  {0.636, 1.272},
	"postgres|db.m6g.4xlarge":  {1.272, 2.544},
	"postgres|db.m6g.8xlarge":  {2.544, 5.088},
	"postgres|db.m6g.large":    {0.159, 0.318},
	"postgres|db.m6g.xlarge":   {0.318, 0.636},
	"postgres|db.m6i.12xlarge": {4.272, 8.544},
	"postgres|db.m6i.16xlarge": {5.696, 11.392},
	"postgres|db.m6i.24xlarge": {8.544, 17.088},
	"postgres|db.m6i.2xlarge":  {0.712, 1.424},
	"postgres|db.m6i.32xlarge": {11.392, 22.784},
	"postgres|db.m6i.4xlarge":  {1.424, 2.848},
	"postgres|db.m6i.8xlarge":  {2.848, 5.696},
	"postgres|db.m6i.large":    {0.178, 0.356},
	"postgres|db.m6i.xlarge":   {0.356, 0.712},
	"postgres|db.m7g.12xlarge": {4.044, 8.088},
	"postgres|db.m7g.16xlarge": {5.392, 10.784},
	"postgres|db.m7g.2xlarge":  {0.674, 1.348},
	"postgres|db.m7g.4xlarge":  {1.348, 2.696},
	"postgres|db.m7g.8xlarge":  {2.696, 5.392},
	"postgres|db.m7g.large":    {0.168, 0.337},
	"postgres|db.m7g.xlarge":   {0.337, 0.674},
	"postgres|db.m7i.12xlarge": {4.272, 8.544},
	"postgres|db.m7i.16xlarge": {5.696, 11.392},
	"postgres|db.m7i.24xlarge": {8.544, 17.088},
	"postgres|db.m7i.2xlarge":  {0.712, 1.424},
	"postgres|db.m7i.48xlarge": {17.088, 34.176},
	"postgres|db.m7i.4xlarge":  {1.424, 2.848},
	"postgres|db.m7i.8xlarge":  {2.848, 5.696},
	"postgres|db.m7i.large":    {0.178, 0.356},
	"postgres|db.m7i.xlarge":   {0.356, 0.712},
	"postgres|db.r3.2xlarge":   {0.995, 1.99},
	"postgres|db.r3.4xlarge":   {1.99, 3.98},
	"postgres|db.r3.8xlarge":   {3.98, 7.96},
	"postgres|db.r3.large":     {0.25, 0.5},
	"postgres|db.r3.xlarge":    {0.5, 1},
	"postgres|db.r4.16xlarge":  {8, 16},
	"postgres|db.r4.2xlarge":   {1, 2},
	"postgres|db.r4.4xlarge":   {2, 4},
	"postgres|db.r4.8xlarge":   {4, 8},
	"postgres|db.r4.large":     {0.25, 0.5},
	"postgres|db.r4.xlarge":    {0.5, 1},
	"postgres|db.r5.12xlarge":  {6, 12},
	"postgres|db.r5.16xlarge":  {8, 16},
	"postgres|db.r5.24xlarge":  {12, 24},
	"postgres|db.r5.2xlarge":   {1, 2},
	"postgres|db.r5.4xlarge":   {2, 4},
	"postgres|db.r5.8xlarge":   {4, 8},
	"postgres|db.r5.large":     {0.25, 0.5},
	"postgres|db.r5.xlarge":    {0.5, 1},
	"postgres|db.r6g.12xlarge": {5.395, 10.79},
	"postgres|db.r6g.16xlarge": {7.194, 14.387},
	"postgres|db.r6g.2xlarge":  {0.899, 1.798},
	"postgres|db.r6g.4xlarge":  {1.798, 3.597},
	"postgres|db.r6g.8xlarge":  {3.597, 7.194},
	"postgres|db.r6g.large":    {0.225, 0.45},
	"postgres|db.r6g.xlarge":   {0.45, 0.899},
	"postgres|db.r6i.12xlarge": {6, 12},
	"postgres|db.r6i.16xlarge": {8, 16},
	"postgres|db.r6i.24xlarge": {12, 24},
	"postgres|db.r6i.2xlarge":  {1, 2},
	"postgres|db.r6i.32xlarge": {16, 32},
	"postgres|db.r6i.4xlarge":  {2, 4},
	"postgres|db.r6i.8xlarge":  {4, 8},
	"postgres|db.r6i.large":    {0.25, 0.5},
	"postgres|db.r6i.xlarge":   {0.5, 1},
	"postgres|db.r7g.12xlarge": {5.738, 11.475},
	"postgres|db.r7g.16xlarge": {7.65, 15.3},
	"postgres|db.r7g.2xlarge":  {0.956, 1.913},
	"postgres|db.r7g.4xlarge":  {1.913, 3.825},
	"postgres|db.r7g.8xlarge":  {3.825, 7.65},
	"postgres|db.r7g.large":    {0.239, 0.478},
	"postgres|db.r7g.xlarge":   {0.478, 0.956},
	"postgres|db.r7i.12xlarge": {6, 12},
	"postgres|db.r7i.16xlarge": {8, 16},
	"postgres|db.r7i.24xlarge": {12, 24},
	"postgres|db.r7i.2xlarge":  {1, 2},
	"postgres|db.r7i.48xlarge": {24, 48},
	"postgres|db.r7i.4xlarge":  {2, 4},
	"postgres|db.r7i.8xlarge":  {4, 8},
	"postgres|db.r7i.large":    {0.25, 0.5},
	"postgres|db.r7i.xlarge":   {0.5, 1},
	"postgres|db.t1.micro":     {0.026, 0.052},
	"postgres|db.t2.2xlarge":   {0.58, 1.16},
	"postgres|db.t2.large":     {0.145, 0.29},
	"postgres|db.t2.medium":    {0.073, 0.146},
	"postgres|db.t2.micro":     {0.018, 0.036},
	"postgres|db.t2.small":     {0.036, 0.072},
	"postgres|db.t2.xlarge":    {0.29, 0.58},
	"postgres|db.t3.2xlarge":   {0.579, 1.158},
	"postgres|db.t3.large":     {0.145, 0.29},
	"postgres|db.t3.medium":    {0.072, 0.145},
	"postgres|db.t3.micro":     {0.018, 0.036},
	"postgres|db.t3.small":     {0.036, 0.072},
	"postgres|db.t3.xlarge":    {0.29, 0.579},
	"postgres|db.t4g.2xlarge":  {0.517, 1.034},
	"postgres|db.t4g.large":    {0.129, 0.258},
	"postgres|db.t4g.medium":   {0.065, 0.129},
	"postgres|db.t4g.micro":    {0.016, 0.032},
	"postgres|db.t4g.small":    {0.032, 0.065},
	"postgres|db.t4g.xlarge":   {0.258, 0.517},
}

// rdsGravitonTwins maps the current x86 families to the Graviton family of
// the same kind and generation.
var rdsGravitonTwins = map[string]string{
	"db.m5": "db.m6g", "db.m6i": "db.m6g", "db.m7i": "db.m7g",
	"db.r5": "db.r6g", "db.r6i": "db.r6g", "db.r7i": "db.r7g",
	"db.t3": "db.t4g",
}

// RDSGravitonTwin is the Graviton class of the same size for a current x86
// class, when the engine version runs on Graviton; "" otherwise.
func RDSGravitonTwin(class, engine, version string) string {
	fam, size, ok := rdsFamily(class)
	g, x86 := rdsGravitonTwins[fam]
	if !ok || !x86 || !gravitonReady(engine, version) {
		return ""
	}
	if twin := g + "." + size; rdsHasClass(twin) && gravitonFamilyReady(g, engine, version) {
		return twin
	}
	return ""
}

// gravitonFamilyReady adds a family's own floors to gravitonReady's shared
// ones. AWS's class-support table (read 2026-11-20) needs PostgreSQL 14.5 or
// later in the 14 line for db.m7g and db.r7g (13.4 and every 15+ are fine,
// and the shared floors already cover them); db.m6g, db.r6g and db.t4g
// take every PostgreSQL 13+ version, and all list only MySQL and MariaDB
// major lines.
func gravitonFamilyReady(family, engine, version string) bool {
	if (family == "db.m7g" || family == "db.r7g") && engine == "postgres" {
		if v := versionParts(version); v[0] == 14 {
			return atLeast(v, 14, 5, 0)
		}
	}
	return true
}

// RDSPreviousGeneration says whether AWS lists the class as previous generation.
func RDSPreviousGeneration(class string) bool {
	fam, _, ok := rdsFamily(class)
	_, old := rdsTwins[fam]
	return ok && old
}

// RDSTwin is the current-generation class of the same size a
// previous-generation class is compared with: Graviton when the engine
// version runs on it, else x86. "" when there is no class of that size.
func RDSTwin(class, engine, version string) (twin string, graviton bool) {
	fam, size, ok := rdsFamily(class)
	t, old := rdsTwins[fam]
	if !ok || !old {
		return "", false
	}
	if g := t[1] + "." + size; gravitonReady(engine, version) && rdsHasClass(g) {
		return g, true
	}
	if x := t[0] + "." + size; rdsHasClass(x) {
		return x, false
	}
	return "", false
}

// RDSUnlimitedVCPUHour is what RDS charges per vCPU-hour above a T3 or T4g
// class's baseline (they always run Unlimited): us-east-1, MySQL, MariaDB
// and PostgreSQL (CPUCredits:db.t3 / db.t4g in the AmazonRDS offer file,
// 2026-11-19).
const RDSUnlimitedVCPUHour = 0.075

// RDSHour is the us-east-1 price per hour of an open-source-engine class
// (mysql, mariadb, postgres) from the table above.
func RDSHour(engine, class string, multiAZ bool) (float64, bool) {
	p, ok := rdsHour[engine+"|"+class]
	if !ok {
		return 0, false
	}
	if multiAZ {
		if p[1] == 0 {
			return 0, false
		}
		return p[1], true
	}
	return p[0], true
}

func rdsFamily(class string) (fam, size string, ok bool) {
	i := strings.LastIndex(class, ".")
	if i <= 0 {
		return "", "", false
	}
	return class[:i], class[i+1:], true
}

// rdsHasClass says whether the class exists: it is priced for at least one
// open-source engine (every class of these families runs MySQL).
func rdsHasClass(class string) bool {
	for _, e := range []string{"mysql", "mariadb", "postgres"} {
		if _, ok := rdsHour[e+"|"+class]; ok {
			return true
		}
	}
	return false
}

// gravitonReady says whether the engine version runs on the Graviton twins.
// The floors are the highest minimums AWS published for its Graviton RDS
// classes in any region (Graviton2 launch, Graviton3, and the 2023 region
// expansion: MySQL 8.0.33 for t4g, PostgreSQL 13.7, 14.3 and 15.2 for
// m6g/r6g, MariaDB 10.11.4 for m7g/r7g), so no version that can't run on
// Graviton somewhere gets one.
func gravitonReady(engine, version string) bool {
	v := versionParts(version)
	switch engine {
	case "mysql":
		return atLeast(v, 8, 0, 33)
	case "mariadb":
		return atLeast(v, 10, 11, 4)
	case "postgres":
		switch {
		case v[0] >= 16:
			return true
		case v[0] == 15:
			return atLeast(v, 15, 2, 0)
		case v[0] == 14:
			return atLeast(v, 14, 3, 0)
		case v[0] == 13:
			return atLeast(v, 13, 7, 0)
		}
	}
	return false
}

func versionParts(s string) [3]int {
	var v [3]int
	for i, p := range strings.SplitN(s, ".", 3) {
		n, err := strconv.Atoi(strings.TrimRightFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			return [3]int{}
		}
		v[i] = n
	}
	return v
}

func atLeast(v [3]int, major, minor, patch int) bool {
	switch {
	case v[0] != major:
		return v[0] > major
	case v[1] != minor:
		return v[1] > minor
	}
	return v[2] >= patch
}
