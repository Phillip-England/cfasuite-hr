# Written in Golang

We are writing a project in golang. This project is an API and it is intended to serve employee data from two (or more) Chick-fil-A locations.

This project is written in Go because go offers easy ability to write good web servers while also being good at being very simple in code design.

We are planning to name this application cfasuite-hr and its repo is located at github.com/phillip-england/cfasuite-hr

The application should be dead simple to get up and running and should not require any external binary deps. It might require libraries, but it should not require any external binaries.

# Sqlite for the Database

We are to make use of sqlite database for this project. Using the cli, we should be able to easily find the location of the database, as well as read tables and rows from tables ect. We should also be able to replicate the database too, that would be very nice.

We need a table for our locations, and for each location we will have employees. So we will need a table for employees too.

Locations have a name and a number, we do not need a lot of information outside of that, just a simple name and a number. Just one thing to note, the location number may start with a 0 like 03394 or something of that nature. That database needs to be able to handle location numbers like that which are invalid numbers in most integer based systems.

Then, employees will have a few more fields that are of importance. Let me explain:

# Employee Data Description

The employee data will not be given to you manually, instead you will be provided with a .csv file that contains all of the employee data. The name of this file is "the employee bio."

The employee bio will contain 12 columns, here are their names:

Employee Name
Employee Number
Employee SSN
Gender
Location
Job
Employee Status
Location Original Start Date
Location Latest Start Date
Termination Date
Termination Reason
Rehire Recommendation

If an employee has a termination date, they are no longer in out business and we need to purge them from our system.

In this way, uploading the employee bio to the system is the way we sync up this system with my actual Chick-fil-A system.

I cannot provide you a direct copy of the employee bio as it contains sensitive data, but I told you enough about the column structure to ensure you can craft code suitable for parsing the file.

So, when we upload the employee bio, we will upload it to a particular Chick-fil-A location. For example, here is the workflow:

1. Admin logs in
2. Admin creates location with specific name and number
3. Admin uploads employee bio for newly created location
4. The system adds employees which do not have a termination date
5. Admin waits a few days while Chick-fil-A system changes
6. Admin downloads new employee bio from Chick-fil-A system
7. Admin uploads new employee bio
8. Our system checks to see if any employees have been terminated or added and syncs our system up with the new version of the employee bio

You will notice employees have an employee number. This is their ID so to speak.

If we mold this system to have data associated with employees, we will do so using the employee number, as it is unique for each employee.

In this way, we can create multiple Chick-fil-A locations and sync our internal data making using of the employee bio.

We do not want to pull EVERY column from the employee bio, but we do want to pull data from the following columns:

Employee Name
Employee Number
Job
Employee Status
Location Latest Start Date

This means our location table will be fairly simple, but the employee table will be more complex as it will contain more data.

Another thought, the Employee Status column will say either "Active" or "Terminated" so we could use that column to determine who should exist in the system and who should be purged or added upon a new upload. That might be more simple as it is fairly straightforward and direct.

I also want to make sure that you understand that the employee bio is associated with a specific location. So, when we upload an employee bio, we are uploading it for a specific Chick-fil-A location. The web ui needs to be very clear on this.

# Admin User

The admin user manages all Chick-fil-A locations and is the only user for this account. The admins username and password is set by environment variables. The cli interface should make setting the admin username and password trivial via commands built into the cli itself.

The admin user can log in, create Chick-fil-A locations, manage those locations (edit them, delete them, ect). The admin can upload employee bio documents (which are in fact not csv files, but instead are .xlsx files).

At ./EmployeeBioReader.py, I have left a python implementation of the employee bio reader so you can see how things were managed in python. Again, I do not want to provide you with a true copy of the employee bio reader, but here is what I will do, I will leave a mock version of the employee bio reader here with fake data so you have something to parse and mess with during development.

The mock employee bio reader is at ./bio.xlsx so you can make use of it.

That gives you two points of reference to properly parse the .xlsx file.

# Api Token Generation

This system can generate and manage api tokens. When generating an API token, you give it a name and then it has a special key associated with it which you can easily copy.

This API token can be used to access the data within this system from other systems.

The cli provides a way for users to get context about how to use the api for large language models. For example, lets imagine you want to create another system which has access to the data within this system, well you can run a special command to put text on your clipboard which can then be provided to a large language model building another project. The text on the clipboard would have information about different endpoints, how to make use of the api token, and how to interact with the system from an external system in order to get data.

This is something the admin manages on their end, and the system makes very clear which api tokens exist in the wild.

# The Employee Data Api

Given a store number and an api token, external systems can access employees and make use of their data. That is the main purpose of this system is to make accessing employee data easy for other systems.

The api should have a documentation page where you can go read about the different endpoints and how they operate. Maybe even you can send mock requests from these endpoints to see how they operate. Of course, all this lives on the admin side of things.

# Employee Birthday Reports

The application supports uploading the Employee Birthday Reader `.xlsx` report for a specific location. The report contains `Employee Name` and `Birth Date` columns. Birthdays are matched to existing employees at the selected location by exact employee name and stored as `YYYY-MM-DD`.

Employees may exist without a birthday. In that case the API returns `birth_date` as `null`. Uploading a new employee bio does not erase existing birthday data for employees who remain active.

The API documentation and generated LLM context must describe how to read birthdays from employee API responses.

# The UI

The UI should be fairly simple as this is not a user-facing application. Only the admin will really interact with the ui.

It should be dark mode by default and have an accent color. But black is the main color with white being secondary color then the accent color reserved for things like buttons or other important ui elements that should stand out in some way.

The ui should favor simplicity and pragmatism while still being easy to use. No need for a single page application, this can be a multi page application that leans towards simplicity.

# Dockerfile

This application should come with a Dockerfile which should make launching this application in its own container super easy. In the README, please provide a section explaining how Docker relates to the sqlite file and how the volume works. I know I will have questions about where my data is stored on disk, what is dictating where it is stored, and all sorts of questions to ensure I understand how my data relates to my Docker instance of the application.

# A Dedicated Port

This application should have a dedicated port and not operate on a common testing port. This is going to be running in the wild and should have its own port that it favors.

# Golang Connection Examples

This application should include in its documentation as well as its context, examples of how to connect to the application using golang.

This makes connecting to this app using other go apps all the more easy. Maybe even a copy/paste library is included with the app that users can simply copy paste into their other projects. Of course, all this is hidden behind the admin view.

# IP Banning Built In

This application will feature a public-facing login form. Because of this, we want to anticipate bots will attempt to break the form. Because of this, we want some sort of self-purging system which will track IP addresses. Here is how it will work:

When a login attempt is made, we will check to see if the IP is banned from the system. If it is not and the login attempt is invalid, we will log the ip in the database and notate that is has 1 invalid login attempt. When 5 invalid attempts are met, the ip is then banned. But, each login attempt, we also check the system for ips which logged in later than 24 hours ago. If we find any ips in the system with logins older than 24 hours, they will be purged from the system.

This allows us to have a database which will ban ips who abuse the system, but whose dataset will not grow forever. It should be self-purging as to prevent massive amounts of login attempts to sit on the system eating away data which is not needed to be eaten.

# Makefile for installation

Please create a makefile and within have a command for 'make install' which will install this application on our system (or whatever system is installing it)

This should make it trivial for someone to just pull down the repo and run make install to get things up and running quickly.
