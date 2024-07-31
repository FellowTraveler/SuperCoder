package auth

import (
	"ai-developer/app/config"
	"ai-developer/app/models"
	"ai-developer/app/services"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	oauthGithub "golang.org/x/oauth2/github"
	"gorm.io/gorm"
	"net/http"
)

type GithubAuthProvider struct {
	AuthProvider
	logger              *zap.Logger
	userService         *services.UserService
	organisationService *services.OrganisationService
	githubOAuthConfig   *config.GithubOAuthConfig
}

func (gap GithubAuthProvider) Authenticate(c *gin.Context) (user interface{}, err error) {
	gap.logger.Debug("Authenticating user with Github")

	c.Redirect(http.StatusFound, gap.githubOAuthConfig.FrontendURL())

	code := c.Query("code")
	githubOauthConfig := &oauth2.Config{
		ClientID:     gap.githubOAuthConfig.ClientId(),
		ClientSecret: gap.githubOAuthConfig.ClientSecret(),
		RedirectURL:  gap.githubOAuthConfig.RedirectURL(),
		Scopes:       []string{"user:email"},
		Endpoint:     oauthGithub.Endpoint,
	}

	token, err := githubOauthConfig.Exchange(context.Background(), code)
	if err != nil {
		gap.logger.Error("Error exchanging code for token", zap.Error(err))
		return
	}

	client := github.NewClient(githubOauthConfig.Client(context.Background(), token))

	emails, _, err := client.Users.ListEmails(context.Background(), nil)
	if err != nil {
		gap.logger.Error("Error fetching user emails", zap.Error(err))
		return
	}

	var primaryEmail string
	for _, email := range emails {
		if email.GetPrimary() {
			primaryEmail = email.GetEmail()
			break
		}
	}

	existingUser, err := gap.userService.GetUserByEmail(primaryEmail)

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		gap.logger.Error("Error fetching user by email", zap.Error(err))
		return
	}

	if existingUser != nil {
		gap.logger.Debug("User authenticated with Github", zap.Any("user", existingUser))
		return existingUser, nil
	}

	gap.logger.Debug("User not found, creating new user")
	err = nil
	var githubUser *github.User
	githubUser, _, err = client.Users.Get(context.Background(), "")
	if err != nil {
		gap.logger.Error("Error fetching user from Github", zap.Error(err))
		return
	}
	return gap.CreateUser(primaryEmail, githubUser)
}

func (gap GithubAuthProvider) CreateUser(email string, githubUser *github.User) (user *models.User, err error) {
	var name string
	if githubUser.Login != nil {
		name = *githubUser.Login
	} else {
		name = "N/A"
	}

	organisation := &models.Organisation{
		Name: gap.organisationService.CreateOrganisationName(),
	}
	_, err = gap.organisationService.CreateOrganisation(organisation)
	if err != nil {
		gap.logger.Error("Error creating organisation", zap.Error(err))
		return
	}

	hashedPassword, err := gap.userService.HashUserPassword(gap.userService.CreatePassword())
	if err != nil {
		gap.logger.Error("Error hashing user password", zap.Error(err))
		return
	}

	user, err = gap.userService.CreateUser(&models.User{
		Name:           name,
		Email:          email,
		OrganisationID: organisation.ID,
		Password:       hashedPassword,
	})

	if err != nil {
		gap.logger.Error("Error creating user", zap.Error(err))
		return
	}

	return
}

func NewGithubAuthProvider(
	githubOAuthConfig *config.GithubOAuthConfig,
	userService *services.UserService,
	organisationService *services.OrganisationService,
	logger *zap.Logger,
) *GithubAuthProvider {
	return &GithubAuthProvider{
		userService:         userService,
		organisationService: organisationService,
		githubOAuthConfig:   githubOAuthConfig,
		logger:              logger.Named("GithubAuthProvider"),
	}
}