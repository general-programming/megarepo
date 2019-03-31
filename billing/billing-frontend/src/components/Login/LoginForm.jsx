import React from 'react';
import { withRouter } from 'react-router-dom';
import PropTypes from 'prop-types';
import Avatar from '@material-ui/core/Avatar';
import Button from '@material-ui/core/Button';
import CssBaseline from '@material-ui/core/CssBaseline';
import FormControl from '@material-ui/core/FormControl';
import Chip from '@material-ui/core/Chip';
import Input from '@material-ui/core/Input';
import InputLabel from '@material-ui/core/InputLabel';
import GithubCircle from 'mdi-material-ui/GithubCircle';
import Paper from '@material-ui/core/Paper';
import Typography from '@material-ui/core/Typography';
import withStyles from '@material-ui/core/styles/withStyles';
import { connect } from 'react-redux';

import compactLogo from '../../assets/img/logo-square-compact.png';

import validateLoginInput from '../../validation/login';
import { login } from '../../actions/user.action';

const styles = theme => ({
    main: {
        width: 'auto',
        display: 'block', // Fix IE 11 issue.
        marginLeft: theme.spacing.unit * 3,
        marginRight: theme.spacing.unit * 3,
        [theme.breakpoints.up(400 + theme.spacing.unit * 3 * 2)]: {
            width: 400,
            marginLeft: 'auto',
            marginRight: 'auto',
        },
    },
    paper: {
        marginTop: theme.spacing.unit * 8,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        padding: `${theme.spacing.unit * 2}px ${theme.spacing.unit * 3}px ${theme.spacing.unit * 3}px`,
    },
    avatar: {
        margin: theme.spacing.unit,
        backgroundColor: theme.palette.primary.light,
    },
    form: {
        width: '100%', // Fix IE 11 issue.
        marginTop: theme.spacing.unit,
    },
    submit: {
        marginTop: theme.spacing.unit * 3,
    },
    button: {
        marginTop: theme.spacing.unit * 3,
    },
    chip: {
        width: '100%',
        color: '#fff',
        backgroundColor: '#f4620c',
    },
});

class LoginForm extends React.Component {
    constructor(props) {
        super(props);

        // maybe remove old login, not sure yet
        // this.props.dispatch(userActions.logout());

        this.state = {
            username: '',
            password: '',
            errors: {},
        };

        this.handleChange = this.handleChange.bind(this);
        this.handleSubmit = this.handleSubmit.bind(this);
    }

    handleChange(e) {
        const { name, value } = e.target;
        this.setState({ [name]: value });
    }

    handleSubmit(e) {
        e.preventDefault();

        const { username, password } = this.state;
        // eslint-disable-next-line react/prop-types
        const { dispatch, history } = this.props;

        const validation = validateLoginInput({ username, password });
        if (!validation.isValid) {
            this.setState({
                errors: validation.errors,
            });
            console.log(validation.errors);
            return;
        }

        dispatch(login(username, password));
    }

    render() {
        const { classes, error, loggingIn } = this.props;

        return (
            <main className={classes.main}>
                <CssBaseline />
                <Paper className={classes.paper}>
                    <Avatar src={compactLogo} className={classes.avatar} />
                    <Typography component="h1" variant="h5">
                        Sign in
                    </Typography>
                    <form className={classes.form} onSubmit={this.handleSubmit}>
                        { error && (<Chip
                            label={`Oh heck! ${error}`}
                            className={classes.chip}
                            color="secondary"
                        />
                        )}
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="username">Username</InputLabel>
                            <Input
                                id="username"
                                name="username"
                                autoComplete="username"
                                autoFocus
                                value={this.state.username}
                                onChange={this.handleChange}
                                error={!!this.state.errors.username}
                            />
                        </FormControl>
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="password">Password</InputLabel>
                            <Input
                                name="password"
                                type="password"
                                id="password"
                                autoComplete="current-password"
                                value={this.state.password}
                                onChange={this.handleChange}
                                error={!!this.state.errors.password}
                            />
                        </FormControl>
                        {/* <FormControlLabel
                            control={<Checkbox value="remember" color="primary" />}
                            label="Remember me"
                        /> */}
                        <Button
                            type="submit"
                            fullWidth
                            variant="contained"
                            color="primary"
                            className={classes.submit}
                            disabled={loggingIn}
                        >
                            Sign in
                        </Button>

                        <Button
                            variant="contained"
                            fullWidth
                            color="default"
                            className={classes.button}
                        >
                            <GithubCircle />
                            Login with Github
                        </Button>
                    </form>
                </Paper>
            </main>
        );
    }
}

LoginForm.propTypes = {
    classes: PropTypes.object.isRequired,
    loggingIn: PropTypes.bool,
    error: PropTypes.string,
};

function mapStateToProps(state) {
    const { loggingIn, error } = state.authentication;
    return {
        loggingIn,
        error,
    };
}

export default connect(mapStateToProps)(withRouter(withStyles(styles)(LoginForm)));
